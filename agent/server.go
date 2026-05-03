package agent

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed ui/*
var uiFS embed.FS

type Server struct {
	cfg        Config
	store      *Store
	openai     *OpenAIClient
	indexTmpl  *template.Template
	uiFiles    fs.FS
	fileServer http.Handler
}

func NewServer(cfg Config) (*Server, error) {
	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	oai, err := NewOpenAIClient(cfg.DefaultModel)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	uiFiles, err := resolveUIFS(cfg.UIDir)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	indexBytes, err := fs.ReadFile(uiFiles, "index.html")
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	tmpl, err := template.New("index").Parse(string(indexBytes))
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	return &Server{
		cfg:        cfg,
		store:      store,
		openai:     oai,
		indexTmpl:  tmpl,
		uiFiles:    uiFiles,
		fileServer: http.FileServer(http.FS(uiFiles)),
	}, nil
}

func resolveUIFS(uiDir string) (fs.FS, error) {
	if strings.TrimSpace(uiDir) == "" {
		return fs.Sub(uiFS, "ui")
	}
	abs, err := filepath.Abs(uiDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrNotExist
	}
	return os.DirFS(abs), nil
}

func (s *Server) Close() error {
	return s.store.Close()
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/conversations", s.handleConversations)
	mux.HandleFunc("/api/conversations/", s.handleConversationByID)
	mux.Handle("/app.js", s.requireHeader(http.HandlerFunc(s.handleStatic)))
	mux.Handle("/styles.css", s.requireHeader(http.HandlerFunc(s.handleStatic)))
	return mux
}

func (s *Server) requireHeader(next http.Handler) http.Handler {
	if s.cfg.RequiredHeader == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(s.cfg.RequiredHeader) == "" {
			http.Error(w, "missing required header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.requireHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = s.indexTmpl.Execute(w, map[string]any{
			"DefaultModel": s.cfg.DefaultModel,
		})
	})).ServeHTTP(w, r)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if _, err := fs.Stat(s.uiFiles, name); err != nil {
		http.NotFound(w, r)
		return
	}
	s.fileServer.ServeHTTP(w, r)
}

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	s.requireHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			list, err := s.store.ListConversations()
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, list)
		case http.MethodPost:
			var req struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			title := strings.TrimSpace(req.Title)
			if title == "" {
				title = "conversation"
			}
			conv, err := s.store.CreateConversation(title, s.cfg.WorkspaceDir)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSONStatus(w, conv, http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})).ServeHTTP(w, r)
}

func (s *Server) handleConversationByID(w http.ResponseWriter, r *http.Request) {
	s.requireHeader(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/api/conversations/")
		parts := strings.Split(strings.Trim(trimmed, "/"), "/")
		if len(parts) == 0 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		id, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		conv, err := s.store.GetConversation(id)
		if err != nil {
			if err == sql.ErrNoRows {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if len(parts) == 1 {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeJSON(w, conv)
			return
		}
		if parts[1] != "messages" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodGet:
			msgs, err := s.store.ListMessages(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSON(w, msgs)
		case http.MethodPost:
			var req struct {
				Content string `json:"content"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			content := strings.TrimSpace(req.Content)
			if content == "" {
				http.Error(w, "content is required", http.StatusBadRequest)
				return
			}
			if _, err := s.store.AddMessage(id, "user", content); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			history, err := s.store.ListMessages(id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			reply, err := s.openai.RunTurn(context.Background(), s.cfg, conv, history, s.store)
			if err != nil {
				reply = "agent error: " + err.Error()
			}
			msg, err := s.store.AddMessage(id, "assistant", reply)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeJSONStatus(w, msg, http.StatusCreated)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})).ServeHTTP(w, r)
}

func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, v, http.StatusOK)
}

func writeJSONStatus(w http.ResponseWriter, v any, code int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
