package main

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type App struct {
	cfg      Config
	db       *sql.DB
	dbPath   string
	renderer *viewRenderer
	sessions *sessionStore
}

func NewApp(cfg Config) (*App, error) {
	if cfg.DataDir == "" {
		return nil, errors.New("data dir is required")
	}
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, err
	}
	db, dbPath, err := openDB(cfg)
	if err != nil {
		return nil, err
	}
	renderer, err := newViewRenderer()
	if err != nil {
		db.Close()
		return nil, err
	}
	app := &App{
		cfg:      cfg,
		db:       db,
		dbPath:   dbPath,
		renderer: renderer,
		sessions: newSessionStore(),
	}
	if err := app.initializeSettings(); err != nil {
		db.Close()
		return nil, err
	}
	if err := app.ensureBaseImage(); err != nil {
		db.Close()
		return nil, err
	}
	return app, nil
}

func (a *App) Close() error {
	return a.db.Close()
}

func (a *App) initializeSettings() error {
	settings, err := loadSettings(a.db, a.cfg)
	if err != nil {
		return err
	}
	if settings.SessionKey == "" {
		if err := saveSetting(a.db, "session_key", randomToken(32)); err != nil {
			return err
		}
	}
	if settings.GlobalPrompt == "" {
		if err := saveSetting(a.db, "global_prompt", defaultGlobalPrompt()); err != nil {
			return err
		}
	}
	if settings.DefaultOpenAIAPIKey == "" {
		if err := saveSetting(a.db, "default_openai_api_key", a.cfg.DefaultOpenAIAPIKey); err != nil {
			return err
		}
	}
	if settings.PasswordHash == "" {
		if a.cfg.AdminPassword == "" {
			return errors.New("admin password is not set; add admin_password to smolvm.config.json for first start")
		}
		hash, err := hashPassword(a.cfg.AdminPassword)
		if err != nil {
			return err
		}
		if err := saveSetting(a.db, "password_hash", hash); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()
	staticFiles, err := embeddedStaticFS()
	if err == nil {
		mux.Handle("/static/", a.requireAuthHandler(http.StripPrefix("/static/", http.FileServer(http.FS(staticFiles)))))
	}
	mux.HandleFunc("/internal/instance-config", a.handleInternalInstanceConfig)
	mux.HandleFunc("/login", a.handleLogin)
	mux.HandleFunc("/logout", a.handleLogout)
	mux.HandleFunc("/api/", a.requireAuth(a.handleShelleyRootProxy))
	mux.HandleFunc("/version-check", a.requireAuth(a.handleShelleyRootProxy))
	mux.HandleFunc("/diffs-worker.js", a.requireAuth(a.handleShelleyRootProxy))
	mux.HandleFunc("/monaco-editor.js", a.requireAuth(a.handleShelleyRootProxy))
	mux.HandleFunc("/monaco-editor.css", a.requireAuth(a.handleShelleyRootProxy))
	mux.HandleFunc("/editor.worker.js", a.requireAuth(a.handleShelleyRootProxy))
	mux.HandleFunc("/instances/", a.requireAuth(a.handleInstanceRoutes))
	mux.HandleFunc("/settings", a.requireAuth(a.handleSettings))
	mux.HandleFunc("/", a.requireAuth(a.handleDashboard))
	return mux
}

func (a *App) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !a.sessions.valid(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func (a *App) requireAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || !a.sessions.valid(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.renderer.render(w, "login.html", LoginData{Title: "Login"})
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderer.render(w, "login.html", LoginData{Title: "Login", Error: err.Error()})
		return
	}
	settings, err := loadSettings(a.db, a.cfg)
	if err != nil {
		a.renderer.render(w, "login.html", LoginData{Title: "Login", Error: err.Error()})
		return
	}
	password := r.FormValue("password")
	if !checkPassword(settings.PasswordHash, password) {
		a.renderer.render(w, "login.html", LoginData{Title: "Login", Error: "Invalid password"})
		return
	}
	setSessionCookie(w, a.sessions.create())
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		a.sessions.delete(cookie.Value)
	}
	clearSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a *App) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	settings, err := loadSettings(a.db, a.cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	instances, err := listInstances(a.db)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var views []InstanceView
	adminHost := requestHost(r)
	for _, inst := range instances {
		status := a.instanceStatus(inst)
		views = append(views, InstanceView{
			Instance:   inst,
			Status:     status,
			ShelleyURL: fmt.Sprintf("/instances/%d/open", inst.ID),
			AppURL:     appURL(adminHost, inst.WebPort),
			CreatedAgo: "",
		})
	}
	a.renderer.render(w, "dashboard.html", DashboardData{
		Title:               "smolvm",
		Auth:                true,
		Instances:           views,
		DefaultOpenAIAPIKey: settings.DefaultOpenAIAPIKey,
		GlobalPrompt:        settings.GlobalPrompt,
		AdminHost:           adminHost,
	})
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := loadSettings(a.db, a.cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := SettingsData{
		Title:               "Settings",
		GlobalPrompt:        settings.GlobalPrompt,
		DefaultOpenAIAPIKey: settings.DefaultOpenAIAPIKey,
	}
	if r.Method == http.MethodGet {
		a.renderer.render(w, "settings.html", data)
		return
	}
	if err := r.ParseForm(); err != nil {
		data.Error = err.Error()
		a.renderer.render(w, "settings.html", data)
		return
	}
	for _, kv := range []struct {
		Key, Value string
	}{
		{"global_prompt", r.FormValue("global_prompt")},
		{"default_openai_api_key", strings.TrimSpace(r.FormValue("default_openai_api_key"))},
	} {
		if err := saveSetting(a.db, kv.Key, kv.Value); err != nil {
			data.Error = err.Error()
			a.renderer.render(w, "settings.html", data)
			return
		}
	}
	if pw := r.FormValue("new_password"); pw != "" {
		hash, err := hashPassword(pw)
		if err != nil {
			data.Error = err.Error()
			a.renderer.render(w, "settings.html", data)
			return
		}
		if err := saveSetting(a.db, "password_hash", hash); err != nil {
			data.Error = err.Error()
			a.renderer.render(w, "settings.html", data)
			return
		}
	}
	data.Success = "Settings saved"
	data.GlobalPrompt = r.FormValue("global_prompt")
	data.DefaultOpenAIAPIKey = strings.TrimSpace(r.FormValue("default_openai_api_key"))
	a.renderer.render(w, "settings.html", data)
}

func (a *App) handleInstanceRoutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/instances/")
	if path == "new" || path == "new/" {
		a.handleNewInstance(w, r)
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 1 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 1 {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	switch parts[1] {
	case "open":
		a.handleOpenShelley(w, r, id)
	case "terminal":
		if len(parts) == 2 {
			a.handleTerminalPage(w, r, id)
			return
		}
		if len(parts) == 3 && parts[2] == "ws" {
			a.handleTerminalWebsocket(w, r, id)
			return
		}
		http.NotFound(w, r)
	case "start":
		a.handleStartInstance(w, r, id)
	case "stop":
		a.handleStopInstance(w, r, id)
	case "delete":
		a.handleDeleteInstance(w, r, id)
	case "logs":
		a.handleLogs(w, r, id)
	case "shelley":
		a.handleShelleyProxy(w, r, id)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) handleOpenShelley(w http.ResponseWriter, r *http.Request, id int64) {
	if _, err := getInstance(a.db, id); err != nil {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "smolvm_instance",
		Value:    strconv.FormatInt(id, 10),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, fmt.Sprintf("/instances/%d/shelley/", id), http.StatusSeeOther)
}

func (a *App) handleShelleyProxy(w http.ResponseWriter, r *http.Request, id int64) {
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", inst.ShelleyPort))
	prefix := fmt.Sprintf("/instances/%d/shelley", id)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		originalHost := req.Host
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = originalHost
		req.Header.Set("X-Forwarded-Host", originalHost)
		req.Header.Set("X-Forwarded-Proto", "http")
		req.Header.Set("X-SmolVM-Admin", "1")
		req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
		if req.URL.Path == "" {
			req.URL.Path = "/"
		}
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		return rewriteShelleyResponse(resp, id)
	}
	proxy.ServeHTTP(w, r)
}

func (a *App) handleShelleyRootProxy(w http.ResponseWriter, r *http.Request) {
	inst, err := a.instanceFromShelleyReferer(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", inst.ShelleyPort))
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Director = func(req *http.Request) {
		originalHost := req.Host
		req.URL.Scheme = target.Scheme
		req.URL.Host = target.Host
		req.Host = originalHost
		req.Header.Set("X-Forwarded-Host", originalHost)
		req.Header.Set("X-Forwarded-Proto", "http")
		req.Header.Set("X-SmolVM-Admin", "1")
	}
	proxy.ServeHTTP(w, r)
}

func (a *App) instanceFromShelleyReferer(r *http.Request) (Instance, error) {
	if cookie, err := r.Cookie("smolvm_instance"); err == nil && cookie.Value != "" {
		if id, err := strconv.ParseInt(cookie.Value, 10, 64); err == nil {
			if inst, err := getInstance(a.db, id); err == nil {
				return inst, nil
			}
		}
	}
	referer := r.Header.Get("Referer")
	if referer == "" {
		return Instance{}, fmt.Errorf("missing referer")
	}
	u, err := url.Parse(referer)
	if err != nil {
		return Instance{}, fmt.Errorf("invalid referer")
	}
	path := strings.TrimPrefix(u.Path, "/instances/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 || parts[1] != "shelley" {
		return Instance{}, fmt.Errorf("not a shelley referer path")
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Instance{}, fmt.Errorf("invalid instance id")
	}
	inst, err := getInstance(a.db, id)
	if err != nil {
		return Instance{}, fmt.Errorf("instance not found")
	}
	return inst, nil
}

func (a *App) handleLogs(w http.ResponseWriter, r *http.Request, id int64) {
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	logs, err := instanceLogs(inst, a.runtimeFor(inst))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(logs))
}

func (a *App) handleStartInstance(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	go func(inst Instance) {
		_ = a.startInstance(inst)
	}(inst)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleStopInstance(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	go func(inst Instance) {
		_ = a.stopInstance(inst)
	}(inst)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (a *App) handleDeleteInstance(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	go func(inst Instance) {
		_ = a.deleteInstance(inst)
	}(inst)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func appURL(host string, port int) string {
	return fmt.Sprintf("http://%s:%d", host, port)
}

func requestHost(r *http.Request) string {
	host := strings.TrimSpace(r.Host)
	if host == "" {
		return "127.0.0.1"
	}
	if strings.HasPrefix(host, "[") {
		if parsedHost, _, err := net.SplitHostPort(host); err == nil {
			return strings.Trim(parsedHost, "[]")
		}
		return strings.Trim(host, "[]")
	}
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		return parsedHost
	}
	return host
}

func (a *App) runtimeFor(inst Instance) InstanceRuntime {
	base := filepath.Join(a.cfg.DataDir, "instances", fmt.Sprintf("%d-%s", inst.ID, inst.Slug))
	return InstanceRuntime{
		MachineName:   fmt.Sprintf("smolvm-%d-%s", inst.ID, inst.Slug),
		InstanceDir:     base,
		DiskImagePath:   filepath.Join(base, "disk.qcow2"),
		SerialLogPath:   filepath.Join(base, "serial.log"),
		PIDPath:         filepath.Join(base, "qemu.pid"),
		SSHPort:         10000 + int(inst.ID),
	}
}
