package controller

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dfwv1alpha1 "github.com/hwchiu/mon/api/dfw/v1alpha1"
	"github.com/hwchiu/mon/pkg/distribution"
	"github.com/hwchiu/mon/pkg/engine"
)

// ZoneReconciler reconciles Zone, GroundRule, ZoneRule and produces PolicyVersion.
type ZoneReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	// Distributor is optional; if set, new PolicyVersion will trigger gRPC push to agents.
	Distributor *distribution.Server
}

func (r *ZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var zones dfwv1alpha1.ZoneList
	if err := r.List(ctx, &zones); err != nil {
		logger.Error(err, "unable to list Zones")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var groundRules dfwv1alpha1.GroundRuleList
	_ = r.List(ctx, &groundRules)

	var zoneRules dfwv1alpha1.ZoneRuleList
	_ = r.List(ctx, &zoneRules)

	// Convert to engine inputs (simplified)
	zoneInputs := make([]engine.Zone, len(zones.Items))
	for i, z := range zones.Items {
		zoneInputs[i] = engine.Zone{ID: z.Spec.ID, Name: z.Spec.Name, CIDRs: z.Spec.CIDRs}
	}
	groundInputs := make([]engine.GroundRule, len(groundRules.Items))
	for i, g := range groundRules.Items {
		groundInputs[i] = engine.GroundRule{From: g.Spec.FromZone, To: g.Spec.ToZone, Port: g.Spec.Ports}
	}
	zoneRuleInputs := make([]engine.ZoneRule, len(zoneRules.Items))
	for i, zr := range zoneRules.Items {
		zoneRuleInputs[i] = engine.ZoneRule{SrcZone: zr.Spec.SrcZone, DstZone: zr.Spec.DstZone, Port: zr.Spec.Ports}
	}

	policy, err := engine.CompileGroundAndZoneRules(groundInputs, zoneRuleInputs, zoneInputs)
	if err != nil {
		logger.Error(err, "engine compile failed")
		return ctrl.Result{}, err
	}

	logger.Info("Compiled new policy version", "version", policy.Version)

	// Serialize the real MapData (4 maps) for the wire (agents will unmarshal to types.MapData then load eBPF).
	mapBytes, _ := json.Marshal(policy.MapData)

	// Create or update a PolicyVersion CR
	pv := &dfwv1alpha1.PolicyVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "policy-" + policy.Version,
		},
		Spec: dfwv1alpha1.PolicyVersionSpec{
			Description: fmt.Sprintf("Auto-compiled from %d zones", len(zones.Items)),
		},
		Status: dfwv1alpha1.PolicyVersionStatus{
			Version:       policy.Version,
			CreatedAt:     metav1.NewTime(policy.CreatedAt),
			GroundHash:    policy.GroundHash,
			ZoneRulesHash: policy.ZoneRuleHash,
			MapDataRef:    "inline:" + policy.Version, // in real: ref to ConfigMap holding the bytes
		},
	}

	if err := r.Create(ctx, pv); err != nil {
		if client.IgnoreAlreadyExists(err) != nil {
			logger.Error(err, "failed to create PolicyVersion")
			return ctrl.Result{}, err
		}
		logger.V(1).Info("PolicyVersion already exists", "version", policy.Version)
	} else {
		logger.Info("Created PolicyVersion CR", "name", pv.Name)
	}

	// If distributor wired, push the map data (signed stub) to connected agents.
	// This closes the loop: CR change -> engine -> PV -> gRPC push -> agent Apply.
	if r.Distributor != nil {
		r.Distributor.PushUpdate("", policy.Version, mapBytes, nil /* sig */)
	}

	return ctrl.Result{}, nil
}

func (r *ZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&dfwv1alpha1.Zone{}).
		Owns(&dfwv1alpha1.GroundRule{}).
		Owns(&dfwv1alpha1.ZoneRule{}).
		Complete(r)
}
