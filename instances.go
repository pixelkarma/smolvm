package main

import (
	"net/http"
)

func (a *App) handleNewInstance(w http.ResponseWriter, r *http.Request) {
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
	defaultInst := Instance{
		Name:     "instance",
		MemoryMB: 512,
		CPUCount: 1,
		DiskMB:   1024,
		WebPort:  nextAvailablePort(instances, 8100, func(i Instance) int { return i.WebPort }),
		APIKey:   "",
	}
	defaultInst.InitialPrompt = buildInstancePrompt(defaultInst)
	data := InstanceFormData{
		Title:               "New Instance",
		Instance:            defaultInst,
		GlobalPrompt:        settings.GlobalPrompt,
		DefaultOpenAIAPIKey: settings.DefaultOpenAIAPIKey,
		DefaultWebPort:      defaultInst.WebPort,
	}
	if r.Method == http.MethodGet {
		a.renderer.render(w, "instance_form.html", data)
		return
	}
	if err := r.ParseForm(); err != nil {
		data.Error = err.Error()
		a.renderer.render(w, "instance_form.html", data)
		return
	}
	inst, err := parseInstanceForm(r)
	if err != nil {
		data.Instance = inst
		data.Error = err.Error()
		a.renderer.render(w, "instance_form.html", data)
		return
	}
	inst.ShelleyPort = nextAvailablePort(instances, 19000, func(i Instance) int { return i.ShelleyPort })
	inst.Slug, err = mustUniqueSlug(a.db, inst.Name)
	if err != nil {
		data.Instance = inst
		data.Error = err.Error()
		a.renderer.render(w, "instance_form.html", data)
		return
	}
	if err := insertInstance(a.db, &inst); err != nil {
		data.Instance = inst
		data.Error = err.Error()
		a.renderer.render(w, "instance_form.html", data)
		return
	}
	if err := a.startInstance(inst, settings); err != nil {
		_ = deleteInstanceRecord(a.db, inst.ID)
		data.Instance = inst
		data.Error = err.Error()
		a.renderer.render(w, "instance_form.html", data)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
