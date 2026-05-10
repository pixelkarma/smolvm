package main

import "net/http"

func (a *App) shutdownInstance(inst Instance) error {
	return qmpSystemPowerdown(a.runtimeFor(inst).QMPPath)
}

func (a *App) handleShutdownInstance(w http.ResponseWriter, r *http.Request, id int64) {
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
		_ = a.shutdownInstance(inst)
	}(inst)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
