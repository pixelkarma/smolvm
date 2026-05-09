package main

import (
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"sync"
	"syscall"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var terminalUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func (a *App) handleTerminalPage(w http.ResponseWriter, r *http.Request, id int64) {
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data := struct {
		Title       string
		Instance    Instance
		WebsocketURL string
	}{
		Title:        "Terminal",
		Instance:     inst,
		WebsocketURL: "/instances/" + strconv.FormatInt(id, 10) + "/terminal/ws",
	}
	a.renderer.render(w, "terminal.html", data)
}

func (a *App) handleTerminalWebsocket(w http.ResponseWriter, r *http.Request, id int64) {
	inst, err := getInstance(a.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if a.instanceStatus(inst) != "running" {
		http.Error(w, "instance is not running", http.StatusConflict)
		return
	}
	conn, err := terminalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	rt := a.runtimeFor(inst)
	args := []string{
		"-tt",
		"-i", a.cfg.GuestSSHKeyPath,
		"-p", strconv.Itoa(rt.SSHPort),
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"root@127.0.0.1",
	}
	cmd := exec.Command("ssh", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("failed to start terminal: "+err.Error()))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	var once sync.Once
	closeAll := func() {
		once.Do(func() {
			_ = conn.Close()
			_ = ptmx.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
	}

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					closeAll()
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\n[terminal closed]\r\n"))
				}
				closeAll()
				return
			}
		}
	}()

	for {
		var msg terminalMessage
		if err := conn.ReadJSON(&msg); err != nil {
			closeAll()
			return
		}
		switch msg.Type {
		case "input":
			if _, err := ptmx.Write([]byte(msg.Data)); err != nil {
				closeAll()
				return
			}
		case "resize":
			_ = pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
		}
	}
}
