package frontend

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	dfwv1alpha1 "github.com/hwchiu/mon/api/dfw/v1alpha1"
	"github.com/hwchiu/mon/pkg/distribution"
)

//go:embed dashboard.html
var content embed.FS

// StartFrontendServer serves a simple dashboard for status and DFW config.
// It proxies CRUD to the K8s API via the provided client.
func StartFrontendServer(addr string, c client.Client, distSrv *distribution.Server) error {
	mux := http.NewServeMux()

	// Serve the main dashboard (single page app style)
	dashboardTmpl, _ := template.ParseFS(content, "dashboard.html")
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		dashboardTmpl.Execute(w, nil)
	})

	// API: status overview
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()

		var zones dfwv1alpha1.ZoneList
		_ = c.List(ctx, &zones)

		var pvs dfwv1alpha1.PolicyVersionList
		_ = c.List(ctx, &pvs)

		agents := distSrv.GetAgents()

		status := map[string]interface{}{
			"zones":           len(zones.Items),
			"agents":          len(agents),
			"policy_versions": len(pvs.Items),
			"timestamp":       time.Now().UTC(),
		}
		writeJSON(w, status)
	})

	// API: list agents (from gRPC registrations)
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		agents := distSrv.GetAgents()
		writeJSON(w, agents)
	})

	// API: list zones
	mux.HandleFunc("/api/zones", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		var list dfwv1alpha1.ZoneList
		if err := c.List(ctx, &list); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, list.Items)
	})

	// API: create zone (simple POST form or JSON)
	mux.HandleFunc("/api/zones/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		var z dfwv1alpha1.Zone
		if err := json.NewDecoder(r.Body).Decode(&z); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		z.Namespace = "" // cluster scoped
		z.SetGroupVersionKind(dfwv1alpha1.GroupVersion.WithKind("Zone"))
		if err := c.Create(context.Background(), &z); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(z)
	})

	// API: list ground rules
	mux.HandleFunc("/api/groundrules", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		var list dfwv1alpha1.GroundRuleList
		if err := c.List(ctx, &list); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, list.Items)
	})

	// API: create ground rule
	mux.HandleFunc("/api/groundrules/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		var gr dfwv1alpha1.GroundRule
		if err := json.NewDecoder(r.Body).Decode(&gr); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		gr.Namespace = ""
		gr.SetGroupVersionKind(dfwv1alpha1.GroupVersion.WithKind("GroundRule"))
		if err := c.Create(context.Background(), &gr); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(gr)
	})

	// Similar for zonerules (abbreviated for skeleton)
	mux.HandleFunc("/api/zonerules", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		var list dfwv1alpha1.ZoneRuleList
		_ = c.List(ctx, &list)
		writeJSON(w, list.Items)
	})

	mux.HandleFunc("/api/zonerules/create", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", 405)
			return
		}
		var zr dfwv1alpha1.ZoneRule
		if err := json.NewDecoder(r.Body).Decode(&zr); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		zr.Namespace = ""
		zr.SetGroupVersionKind(dfwv1alpha1.GroupVersion.WithKind("ZoneRule"))
		if err := c.Create(context.Background(), &zr); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(zr)
	})

	// API: list current policy versions
	mux.HandleFunc("/api/policies", func(w http.ResponseWriter, r *http.Request) {
		ctx := context.Background()
		var list dfwv1alpha1.PolicyVersionList
		_ = c.List(ctx, &list)
		writeJSON(w, list.Items)
	})

	log.Printf("DFW Frontend + API listening on %s (status, agents, config CRUD)", addr)
	return http.ListenAndServe(addr, mux)
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

