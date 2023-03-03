/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/janosmiko/tutorial-kubebuilder/api/v1alpha1"
)

const (
	DomainResolverFinalizerName = "domainresolver.tutorial.janosmiko.com/finalizer"

	DomainResolverReasonTerminating            = "DomainResolverTerminating"
	DomainResolverReasonNew                    = "DomainResolverNew"
	DomainResolverReasonSpecChanged            = "DomainResolverSpecChanged"
	DomainResolverReasonReset                  = "DomainResolverReset"
	DomainResolverReasonWaitingForDependencies = "DomainResolverWaitingForDependencies"
	DomainResolverReasonCreating               = "DomainResolverCreating"
	DomainResolverReasonCreated                = "DomainResolverCreated"
	DomainResolverReasonDeleted                = "DomainResolverTerminated"
)

// DomainResolverReconciler reconciles a DomainResolver resource
type DomainResolverReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Logger   logr.Logger
	Recorder record.EventRecorder
	Tracer   trace.Tracer
}

//+kubebuilder:rbac:groups=tutorial.janosmiko.com,resources=domainresolvers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=tutorial.janosmiko.com,resources=domainresolvers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=tutorial.janosmiko.com,resources=domainresolvers/finalizers,verbs=update
//+kubebuilder:rbac:groups="*",resources=pods,verbs=get;list

func (r *DomainResolverReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	o := &v1alpha1.DomainResolver{}
	err := r.Get(ctx, req.NamespacedName, o)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Add a deadline just to make sure we don't get stuck in a loop
	ctx, cancel := context.WithDeadline(ctx, metav1.Now().Add(60*time.Second))
	defer cancel()

	// Initialize the tracer
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile")
	defer span.End()

	// Initialize the logger
	log := log.FromContext(ctx)

	log.Info("Reconciling DomainResolver")

	// examine DeletionTimestamp to determine if resource is under deletion
	if !o.ObjectMeta.DeletionTimestamp.IsZero() &&
		v1alpha1.DomainResolverStatusPhase(o.Status.Phase.Type) != v1alpha1.DomainResolverStatusPhaseTerminating {
		span.AddEvent("DomainResolver is being deleted, setting it's status to terminating")
		log.Info("DomainResolver is being deleted, setting it's status to terminating")

		err := r.statusNext(
			ctx,
			o,
			v1alpha1.DomainResolverStatusPhaseTerminating,
			"DomainResolver terminating",
			DomainResolverReasonTerminating,
			false,
		)
		if err != nil {
			span.RecordError(err)

			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	if !controllerutil.ContainsFinalizer(o, DomainResolverFinalizerName) {
		span.AddEvent("Adding finalizer to the DomainResolver")
		log.Info("Adding finalizer to the DomainResolver")

		patch := client.MergeFrom(o.DeepCopy())
		controllerutil.AddFinalizer(o, DomainResolverFinalizerName)

		err = r.Client.Patch(ctx, o, patch)
		if err != nil {
			span.RecordError(err)

			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	if r.checkTrigger(o) {
		return r.ReconcilePending(ctx, o)
	}

	switch v1alpha1.DomainResolverStatusPhase(o.Status.Phase.Type) {
	case "":
		return r.ReconcileNew(ctx, o)

	case v1alpha1.DomainResolverStatusPhasePending:
		return r.ReconcilePending(ctx, o)

	case v1alpha1.DomainResolverStatusPhaseCreating:
		return r.ReconcileCreating(ctx, o)

	case v1alpha1.DomainResolverStatusPhaseCreated:
		return r.ReconcileCreated(ctx, o)

	case v1alpha1.DomainResolverStatusPhaseTerminating:
		return r.ReconcileTerminating(ctx, o)

	case v1alpha1.DomainResolverStatusPhaseDeleted:
		return r.ReconcileDeleted(ctx, o)

	case v1alpha1.DomainResolverStatusPhaseError:
		return r.ReconcileError(ctx, o)

	default:
		return ctrl.Result{}, fmt.Errorf("unknown status: %s", o.Status.Phase.Type)
	}
}

func (r *DomainResolverReconciler) ReconcileNew(ctx context.Context, o *v1alpha1.DomainResolver) (ctrl.Result, error) {
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile-new")
	defer span.End()

	log := log.FromContext(ctx)

	span.AddEvent("DomainResolver is in new state, setting to pending")
	log.Info("DomainResolver is in new state, setting to pending")

	err := r.statusNext(
		ctx,
		o,
		v1alpha1.DomainResolverStatusPhasePending,
		"Preparing DomainResolver",
		DomainResolverReasonNew,
		false,
	)
	if err != nil {
		span.RecordError(err)

		return ctrl.Result{}, err
	}

	return r.ReconcilePending(ctx, o)
}

func (r *DomainResolverReconciler) ReconcilePending(ctx context.Context, o *v1alpha1.DomainResolver) (
	ctrl.Result, error) {
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile-pending")
	defer span.End()

	log := log.FromContext(ctx)

	span.AddEvent("DomainResolver is in pending state - checking dependencies")
	log.Info("DomainResolver is in pending state - checking dependencies")

	// We can check here if any of the dependencies are not ready and update status to waiting
	// if err := r.statusNext(
	// 	ctx,
	// 	o,
	// 	v1alpha1.DomainResolverStatusPhasePending,
	// 	"DomainResolver pending",
	// 	DomainResolverReasonWaitingForDependencies,
	// 	false,
	// ); err != nil {
	// 	return ctrl.Result{}, err
	// }

	if err := r.updateSpecHash(ctx, o); err != nil {
		span.RecordError(err)

		return ctrl.Result{}, err
	}

	span.AddEvent("Creating DomainResolver")
	log.Info("Creating DomainResolver")

	if err := r.statusNext(
		ctx,
		o,
		v1alpha1.DomainResolverStatusPhaseCreating,
		"DomainResolver creating",
		DomainResolverReasonCreating,
		false,
	); err != nil {
		return ctrl.Result{}, err
	}

	return r.ReconcileCreating(ctx, o)
}

func (r *DomainResolverReconciler) ReconcileCreating(ctx context.Context, o *v1alpha1.DomainResolver) (
	ctrl.Result, error) {
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile-creating")
	defer span.End()

	log := log.FromContext(ctx)

	span.AddEvent("DomainResolver is in creating state")
	log.Info("DomainResolver is in creating state")

	// Add the main creation logic here
	// Fetch the IP Address of the website stored in the resource's spec
	ip, err := net.LookupIP(o.Spec.Domain)
	if err != nil {
		return ctrl.Result{}, err
	}

	pods := &corev1.PodList{}
	_ = r.List(
		ctx, pods,
		client.MatchingFields(fields.Set{".customIndexer.pods.byFirstContainerName": "kube-rbac-proxy"}),
	)
	if len(pods.Items) == 1 {
		o.Status.ControllerImageID = pods.Items[0].Status.ContainerStatuses[0].ImageID
	}

	// Update the status of the resource with the IP address
	o.Status.IPAddress = ip[0].String()
	err = r.Client.Status().Update(ctx, o)
	if err != nil {
		span.RecordError(err)

		return ctrl.Result{}, err
	}

	span.AddEvent("DomainResolver is created")
	log.Info("DomainResolver is created")

	if err := r.statusNext(
		ctx,
		o,
		v1alpha1.DomainResolverStatusPhaseCreated,
		"DomainResolver created",
		DomainResolverReasonCreated,
		false,
	); err != nil {
		return ctrl.Result{}, err
	}

	return r.ReconcileCreated(ctx, o)
}

func (r *DomainResolverReconciler) ReconcileCreated(ctx context.Context, o *v1alpha1.DomainResolver) (
	ctrl.Result, error) {
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile-created")
	defer span.End()

	log := log.FromContext(ctx)

	specHash, err := o.CreateSpecHash()
	if err != nil {
		span.RecordError(err)

		return ctrl.Result{}, err
	}

	if o.Status.SpecHash != specHash {
		err = r.statusNext(
			ctx,
			o,
			v1alpha1.DomainResolverStatusPhasePending,
			"DomainResolver spec changed, resetting to pending",
			DomainResolverReasonSpecChanged,
			false,
		)
		if err != nil {
			span.RecordError(err)

			return ctrl.Result{}, err
		}

		return ctrl.Result{Requeue: true}, nil
	}

	err = r.statusNext(
		ctx,
		o,
		v1alpha1.DomainResolverStatusPhaseCreated,
		"DomainResolver created",
		DomainResolverReasonCreated,
		true,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	span.AddEvent("DomainResolver is in the expected state, stopping reconciliation")
	log.Info("DomainResolver is in the expected state, stopping reconciliation")

	return ctrl.Result{}, nil
}

func (r *DomainResolverReconciler) ReconcileTerminating(ctx context.Context, o *v1alpha1.DomainResolver) (
	ctrl.Result, error) {
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile-terminating")
	defer span.End()

	log := log.FromContext(ctx)

	span.AddEvent("DomainResolver is in terminating state, deleting")
	log.Info("DomainResolver is in terminating state, deleting")

	// The resource is being deleted
	if controllerutil.ContainsFinalizer(o, DomainResolverFinalizerName) {
		if o.Status.SpecHash != "" {
			// If we made any external calls, add the logic here to delete the external resources
			// associated with this resource.
		}
	}

	span.AddEvent("DomainResolver terminated")
	log.Info("DomainResolver terminated")

	err := r.statusNext(
		ctx,
		o,
		v1alpha1.DomainResolverStatusPhaseDeleted,
		"DomainResolver deleted",
		DomainResolverReasonDeleted,
		false,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	return r.ReconcileDeleted(ctx, o)
}

func (r *DomainResolverReconciler) ReconcileDeleted(ctx context.Context, o *v1alpha1.DomainResolver) (
	ctrl.Result, error) {
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile-deleted")
	defer span.End()

	log := log.FromContext(ctx)

	span.AddEvent("DomainResolver is in deleted state, removing finalizer")
	log.Info("DomainResolver is in deleted state, removing finalizer")

	if controllerutil.ContainsFinalizer(o, DomainResolverFinalizerName) {
		controllerutil.RemoveFinalizer(o, DomainResolverFinalizerName)
		err := r.Client.Update(ctx, o)
		if err != nil {
			span.RecordError(err)

			return ctrl.Result{}, err
		}
	}

	span.AddEvent("DomainResolver deleted")
	log.Info("DomainResolver deleted")

	return ctrl.Result{}, nil
}

func (r *DomainResolverReconciler) ReconcileError(ctx context.Context, o *v1alpha1.DomainResolver) (
	ctrl.Result, error) {
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile-error")
	defer span.End()

	log := log.FromContext(ctx)

	// If more than 3 errors have occurred, we stop reconciling
	if o.Status.Failed >= 3 {
		span.AddEvent("DomainResolver is in error state, backoff limit reached")
		log.Info("DomainResolver is in error state, backoff limit reached")

		return ctrl.Result{}, nil
	}

	span.AddEvent("DomainResolver is in error state, resetting to pending")
	log.Info("DomainResolver is in error state, resetting to pending")

	err := r.statusNext(
		ctx,
		o,
		v1alpha1.DomainResolverStatusPhasePending,
		"DomainResolver in error state, resetting to pending",
		DomainResolverReasonReset,
		false,
	)
	if err != nil {
		span.RecordError(err)

		return ctrl.Result{}, err
	}

	return r.ReconcilePending(ctx, o)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DomainResolverReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&corev1.Pod{},
		".customIndexer.pods.byFirstContainerName",
		func(rawObj client.Object) []string {
			pod, ok := rawObj.(*corev1.Pod)
			if !ok {
				return nil
			}

			return []string{pod.Spec.Containers[0].Name}
		},
	)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.DomainResolver{}).
		Complete(r)
}

func (r *DomainResolverReconciler) statusNext(
	ctx context.Context,
	o *v1alpha1.DomainResolver,
	Type v1alpha1.DomainResolverStatusPhase,
	Message, Reason string,
	Ready bool,
) error {
	// Initialize the tracer
	ctx, span := r.Tracer.Start(ctx, "domainresolver-reconcile")
	defer span.End()

	// Initialize the logger
	log := log.FromContext(ctx)

	patch := client.MergeFrom(o.DeepCopy())

	isNew := len(o.Status.Conditions) == 0
	if !isNew {
		o.Status.Conditions[0].Status = metav1.ConditionFalse
	}

	t := string(Type)
	o.Status.Phase = metav1.Condition{
		Type:               t,
		Status:             metav1.ConditionTrue,
		Message:            Message,
		Reason:             Reason,
		LastTransitionTime: metav1.Now(),
	}

	conditionLimit := 10
	switch {
	case len(o.Status.Conditions) > conditionLimit && o.Status.Conditions[0].Type == t:
		o.Status.Conditions = append(
			[]metav1.Condition{o.Status.Phase}, o.Status.Conditions[1:conditionLimit]...,
		)
	case len(o.Status.Conditions) > conditionLimit && o.Status.Conditions[0].Type != t:
		o.Status.Conditions = append(
			[]metav1.Condition{o.Status.Phase}, o.Status.Conditions[:(conditionLimit-1)]...,
		)
	case !isNew && o.Status.Conditions[0].Type == t:
		o.Status.Conditions = append([]metav1.Condition{o.Status.Phase}, o.Status.Conditions[1:]...)
	default:
		o.Status.Conditions = append([]metav1.Condition{o.Status.Phase}, o.Status.Conditions...)
	}

	// If the resource is in error, increase the failed counter
	if Type == v1alpha1.DomainResolverStatusPhaseError {
		o.Status.Failed++
	}

	// If the resource is created, reset the failed counter
	if Type == v1alpha1.DomainResolverStatusPhaseCreated {
		o.Status.Failed = 0
	}

	o.Status.Ready = Ready

	if isNew {
		err := r.Client.Status().Update(ctx, o)
		if err != nil {
			span.RecordError(err)

			return err
		}

		return nil
	}

	err := r.Client.Status().Patch(ctx, o, patch)
	if err != nil {
		span.RecordError(err)

		return err
	}

	span.AddEvent("Phase updated " + Reason)
	log.Info("Phase updated " + Reason)

	// If an error occurred, send a warning event
	if Type == v1alpha1.DomainResolverStatusPhaseError {
		r.Recorder.Event(o, corev1.EventTypeWarning, Reason, Message)

		return nil
	}

	r.Recorder.Event(o, corev1.EventTypeNormal, Reason, Message)

	return nil
}

func (r *DomainResolverReconciler) updateSpecHash(ctx context.Context, o *v1alpha1.DomainResolver) error {
	specHash, err := o.CreateSpecHash()
	if err != nil {
		return err
	}

	o.Status.SpecHash = specHash
	err = r.statusUpdate(ctx, o)
	if err != nil {
		return err
	}

	return nil
}

func (r *DomainResolverReconciler) statusUpdate(ctx context.Context, o *v1alpha1.DomainResolver) error {
	// Patching would change the spechash, so we need to update instead
	err := r.Client.Status().Update(ctx, o)
	if err != nil {

		return err
	}

	return nil
}

func (r *DomainResolverReconciler) checkTrigger(o *v1alpha1.DomainResolver) bool {
	reset := o.GetAnnotations()["tutorial.janosmiko.com/reset"]

	// Parse date from trigger annotation if it's formatted: `date -Iseconds -r 1533415339`
	layout := time.RFC3339
	t, err := time.Parse(layout, reset)
	if err != nil {
		return false
	}

	// Check if trigger is newer than the last transition time
	if t.After(o.Status.Phase.LastTransitionTime.Time) {
		return true
	}

	return false
}
