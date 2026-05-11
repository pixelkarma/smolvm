package main

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRe.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func defaultGlobalPrompt() string {
	return `Global smolvm guidance:
- You are running inside a managed Alpine Linux QEMU virtual machine.
- The admin system exposes your project web server directly on the assigned project web port.
- The agent UI itself is private behind the admin proxy. Do not ask the user to browse the raw private agent port.
- Prefer lightweight dependencies and workflows appropriate for Alpine Linux.
- Be mindful of the assigned CPU, RAM, and disk limits.`
}

func buildInstancePrompt(inst Instance) string {
	return fmt.Sprintf(`Instance environment:
- OS: Alpine Linux
- Architecture: %s
- Private agent port: 9000
- Guest web port: 80
- Host web port: %d
- CPU limit: %d
- Memory limit: %d MB
- Writable disk: %d MB

Instructions:
- If you run a web app, bind it to 0.0.0.0:80
- Assume users will reach the app over the assigned host port %d, which is forwarded to guest port 80
- Use Bash when needed, but keep Alpine compatibility in mind
- Conserve memory and disk when selecting tooling and dependencies`, runtime.GOARCH, inst.WebPort, inst.CPUCount, inst.MemoryMB, inst.DiskMB, inst.WebPort)
}

func parseInstanceForm(r *http.Request) (Instance, error) {
	var inst Instance
	inst.Name = strings.TrimSpace(r.FormValue("name"))
	inst.APIKey = strings.TrimSpace(r.FormValue("api_key"))
	inst.InitialPrompt = strings.TrimSpace(r.FormValue("initial_prompt"))
	var err error
	if inst.MemoryMB, err = strconv.Atoi(r.FormValue("memory_mb")); err != nil {
		return inst, fmt.Errorf("invalid memory")
	}
	if inst.CPUCount, err = strconv.Atoi(r.FormValue("cpu_count")); err != nil {
		return inst, fmt.Errorf("invalid CPU")
	}
	if inst.DiskMB, err = strconv.Atoi(r.FormValue("disk_mb")); err != nil {
		return inst, fmt.Errorf("invalid disk")
	}
	if inst.WebPort, err = strconv.Atoi(r.FormValue("web_port")); err != nil {
		return inst, fmt.Errorf("invalid web port")
	}
	return inst, nil
}

func rewriteAgentResponse(resp *http.Response, id int64) error {
	ct := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ct)
	if mediaType != "text/html" &&
		mediaType != "application/javascript" &&
		mediaType != "text/javascript" &&
		mediaType != "text/css" &&
		mediaType != "application/json" {
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	prefix := fmt.Sprintf("/instances/%d/agent", id)
	rewritten := strings.ReplaceAll(string(body), "/api/", prefix+"/api/")
	rewritten = strings.ReplaceAll(rewritten, `"/api`, `"`+prefix+`/api`)
	rewritten = strings.ReplaceAll(rewritten, `'/api`, `'`+prefix+`/api`)
	rewritten = strings.ReplaceAll(rewritten, `"/version-check`, `"`+prefix+`/version-check`)
	rewritten = strings.ReplaceAll(rewritten, `'/version-check`, `'`+prefix+`/version-check`)
	rewritten = strings.ReplaceAll(rewritten, `"/diffs-worker.js`, `"`+prefix+`/diffs-worker.js`)
	rewritten = strings.ReplaceAll(rewritten, `'/diffs-worker.js`, `'`+prefix+`/diffs-worker.js`)
	rewritten = strings.ReplaceAll(rewritten, `"/monaco-editor.js`, `"`+prefix+`/monaco-editor.js`)
	rewritten = strings.ReplaceAll(rewritten, `'/monaco-editor.js`, `'`+prefix+`/monaco-editor.js`)
	rewritten = strings.ReplaceAll(rewritten, `"/monaco-editor.css`, `"`+prefix+`/monaco-editor.css`)
	rewritten = strings.ReplaceAll(rewritten, `'/monaco-editor.css`, `'`+prefix+`/monaco-editor.css`)
	rewritten = strings.ReplaceAll(rewritten, `"/editor.worker.js`, `"`+prefix+`/editor.worker.js`)
	rewritten = strings.ReplaceAll(rewritten, `'/editor.worker.js`, `'`+prefix+`/editor.worker.js`)
	repl := strings.NewReplacer(
		`href="/`, `href="`+prefix+`/`,
		`src="/`, `src="`+prefix+`/`,
		`action="/`, `action="`+prefix+`/`,
		`//${window.location.host}`+prefix+prefix+`/api/`, `//${window.location.host}`+prefix+`/api/`,
		`window.location.pathname === "/new"`, `window.location.pathname === "`+prefix+`/new"`,
		`window.location.pathname === "/inbox"`, `window.location.pathname === "`+prefix+`/inbox"`,
	)
	rewritten = repl.Replace(rewritten)
	resp.Body = io.NopCloser(strings.NewReader(rewritten))
	resp.ContentLength = int64(len(rewritten))
	resp.Header.Set("Content-Length", strconv.Itoa(len(rewritten)))
	return nil
}

func safeJoin(base string, elem ...string) string {
	parts := append([]string{base}, elem...)
	return filepath.Join(parts...)
}
