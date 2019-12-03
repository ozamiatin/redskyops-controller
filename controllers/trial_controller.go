/*
Copyright 2019 GramLabs, Inc.

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

package controllers

import (
	"context"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/redskyops/k8s-experiment/internal/controller"
	"github.com/redskyops/k8s-experiment/internal/meta"
	"github.com/redskyops/k8s-experiment/internal/trial"
	redskyv1alpha1 "github.com/redskyops/k8s-experiment/pkg/apis/redsky/v1alpha1"
	"github.com/redskyops/k8s-experiment/pkg/controller/metric"
	redskytrial "github.com/redskyops/k8s-experiment/pkg/controller/trial"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// TrialReconciler reconciles a Trial object
type TrialReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	// Keep the raw API reader for doing stabilization checks. In that case we only have patch/get permissions
	// on the object and if we were to use the standard caching reader we would hang because cache itself also
	// requires list/watch. If we ever get a way to disable the cache or the cache becomes smart enough to handle
	// permission errors without hanging we can go back to using standard reader.
	apiReader client.Reader
}

func (r *TrialReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.apiReader = mgr.GetAPIReader()
	return ctrl.NewControllerManagedBy(mgr).
		For(&redskyv1alpha1.Trial{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=batch;extensions,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services,verbs=list
// +kubebuilder:rbac:groups=redskyops.dev,resources=trials,verbs=get;list;watch;create;update;patch;delete

func (r *TrialReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("trial", req.NamespacedName)
	now := metav1.Now()

	// Fetch the Trial instance
	t := &redskyv1alpha1.Trial{}
	if err := r.Get(ctx, req.NamespacedName, t); err != nil {
		return ctrl.Result{}, controller.IgnoreNotFound(err)
	}

	// If we are finished or deleted there is nothing for us to do
	if trial.IsFinished(t) || !t.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Wait for a stable (ish) state
	var waitForStability time.Duration
	for i := range t.Spec.PatchOperations {
		p := &t.Spec.PatchOperations[i]
		if !p.Wait {
			// We have already reached stability for this patch (eventually the whole list should be in this state)
			continue
		}

		// This is the only place we should be using `apiReader`
		if err := redskytrial.WaitForStableState(r.apiReader, ctx, log, p); err != nil {
			// Record the largest retry delay, but continue through the list looking for show stoppers
			if serr, ok := err.(*redskytrial.StabilityError); ok && serr.RetryAfter > 0 {
				if serr.RetryAfter > waitForStability {
					trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialStable, corev1.ConditionFalse, "Waiting", err.Error(), &now)
					waitForStability = serr.RetryAfter
				}
				continue
			}

			// Fail the trial since we couldn't detect a stable state
			trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialFailed, corev1.ConditionTrue, "WaitFailed", err.Error(), &now)
			return r.forTrialUpdate(t, ctx, log)
		}

		// Mark that we have successfully waited for this patch
		p.Wait = false

		// Either overwrite the "waiting" reason from an earlier iteration or change the status from "unknown" to "false"
		trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialStable, corev1.ConditionFalse, "", "", &now)
		return r.forTrialUpdate(t, ctx, log)
	}

	// Remaining patches require a delay; update the trial and adjust the response
	if waitForStability > 0 {
		rr, re := r.forTrialUpdate(t, ctx, log)
		if re == nil {
			rr.RequeueAfter = waitForStability
		}
		return rr, re
	}

	// If there is a stable condition that is not yet true, update the status
	if cc, ok := trial.CheckCondition(&t.Status, redskyv1alpha1.TrialStable, corev1.ConditionTrue); ok && !cc {
		trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialStable, corev1.ConditionTrue, "", "", &now)
		return r.forTrialUpdate(t, ctx, log)
	}

	// TODO Remove this once we know why it is actually needed
	for _, c := range t.Status.Conditions {
		if c.Type == redskyv1alpha1.TrialStable && c.LastTransitionTime.Add(1*time.Second).After(now.Time) {
			return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
	}

	// Find jobs labeled for this trial
	list := &batchv1.JobList{}
	matchingSelector, err := meta.MatchingSelector(t.GetJobSelector())
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.List(ctx, list, matchingSelector); err != nil {
		return ctrl.Result{}, err
	}

	// Update the trial run status using the job status
	needsJob := true
	for i := range list.Items {
		// Setup jobs always have "role=trialSetup" so ignore jobs with that label
		// NOTE: We do not use label selectors on search because we don't know if they are user modified
		if list.Items[i].Labels[redskyv1alpha1.LabelTrialRole] != "trialSetup" {
			if update, requeue := applyJobStatus(r, t, &list.Items[i], &now); update {
				return r.forTrialUpdate(t, ctx, log)
			} else if requeue {
				// We are watching jobs, not pods; we may need to poll the pod state before it is consistent
				return ctrl.Result{Requeue: true}, nil
			}
			needsJob = false
		}
	}

	// Create a trial run job if needed
	if needsJob {
		job := trial.NewJob(t)
		if err := controllerutil.SetControllerReference(t, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		err = r.Create(ctx, job)
		return ctrl.Result{}, err
	}

	// The completion time will be non-nil as soon as the (a?) trial run job finishes
	if t.Status.CompletionTime != nil {
		e := &redskyv1alpha1.Experiment{}
		if err = r.Get(ctx, t.ExperimentNamespacedName(), e); err != nil {
			return ctrl.Result{}, err
		}

		// If we have metrics to collect, use an unknown status to fill the gap (e.g. TCP timeout) until the transition to false
		if len(e.Spec.Metrics) > 0 {
			if _, ok := trial.CheckCondition(&t.Status, redskyv1alpha1.TrialObserved, corev1.ConditionUnknown); !ok {
				trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialObserved, corev1.ConditionUnknown, "", "", &now)
				return r.forTrialUpdate(t, ctx, log)
			}
		}

		// Determine the namespace used to get metric targets
		ns := t.Spec.TargetNamespace
		if ns == "" {
			ns = t.Namespace
		}

		// Look for metrics that have not been collected yet
		for _, m := range e.Spec.Metrics {
			v := findOrCreateValue(t, m.Name)
			if v.AttemptsRemaining == 0 {
				continue
			}

			// Capture the metric
			var captureError error
			if target, err := getMetricTarget(r, ctx, ns, &m); err != nil {
				captureError = err
			} else if value, stddev, err := metric.CaptureMetric(&m, t, target); err != nil {
				if merr, ok := err.(*metric.CaptureError); ok && merr.RetryAfter > 0 {
					// Do not count retries against the remaining attempts
					return ctrl.Result{RequeueAfter: merr.RetryAfter}, nil
				}
				captureError = err
			} else {
				v.AttemptsRemaining = 0
				v.Value = strconv.FormatFloat(value, 'f', -1, 64)
				if stddev != 0 {
					v.Error = strconv.FormatFloat(stddev, 'f', -1, 64)
				}
			}

			// Handle any errors the occurred while collecting the value
			if captureError != nil && v.AttemptsRemaining > 0 {
				v.AttemptsRemaining = v.AttemptsRemaining - 1
				if v.AttemptsRemaining == 0 {
					trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialFailed, corev1.ConditionTrue, "MetricFailed", captureError.Error(), &now)
					if merr, ok := captureError.(*metric.CaptureError); ok {
						// Metric errors contain additional information which should be logged for debugging
						log.Error(err, "Metric collection failed", "address", merr.Address, "query", merr.Query, "completionTime", merr.CompletionTime)
					}
				}
			}

			// Set the observed condition to false since we have observed at least one, but possibly not all of, the metrics
			trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialObserved, corev1.ConditionFalse, "", "", &now)
			return r.forTrialUpdate(t, ctx, log)
		}

		// If all of the metrics are collected, finish the observation
		if cc, ok := trial.CheckCondition(&t.Status, redskyv1alpha1.TrialObserved, corev1.ConditionTrue); ok && !cc {
			trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialObserved, corev1.ConditionTrue, "", "", &now)
		}

		// Mark the trial as completed
		trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialComplete, corev1.ConditionTrue, "", "", &now)
		return r.forTrialUpdate(t, ctx, log)
	}

	// Nothing changed
	return ctrl.Result{}, nil
}

// Returns from the reconcile loop after updating the supplied trial instance
func (r *TrialReconciler) forTrialUpdate(t *redskyv1alpha1.Trial, ctx context.Context, log logr.Logger) (ctrl.Result, error) {
	// If we are going to be updating the trial, make sure the status is synchronized (ignore errors)
	_ = trial.UpdateStatus(t)

	result, err := controller.RequeueConflict(r.Update(ctx, t))
	return *result, err
}

func getMetricTarget(r client.Reader, ctx context.Context, namespace string, m *redskyv1alpha1.Metric) (runtime.Object, error) {
	switch m.Type {
	case redskyv1alpha1.MetricPods:
		// Use the selector to get a list of pods
		target := &corev1.PodList{}
		if sel, err := meta.MatchingSelector(m.Selector); err != nil {
			return nil, err
		} else if err := r.List(ctx, target, client.InNamespace(namespace), sel); err != nil {
			return nil, err
		}
		return target, nil
	case redskyv1alpha1.MetricPrometheus, redskyv1alpha1.MetricJSONPath:
		// Both Prometheus and JSONPath target a service
		// NOTE: This purposely ignores the namespace in case Prometheus is running cluster wide
		target := &corev1.ServiceList{}
		if sel, err := meta.MatchingSelector(m.Selector); err != nil {
			return nil, err
		} else if err := r.List(ctx, target, sel); err != nil {
			return nil, err
		}
		return target, nil
	default:
		// Assume no target is necessary
		return nil, nil
	}
}

func findOrCreateValue(trial *redskyv1alpha1.Trial, name string) *redskyv1alpha1.Value {
	for i := range trial.Spec.Values {
		if trial.Spec.Values[i].Name == name {
			return &trial.Spec.Values[i]
		}
	}

	trial.Spec.Values = append(trial.Spec.Values, redskyv1alpha1.Value{Name: name, AttemptsRemaining: 3})
	return &trial.Spec.Values[len(trial.Spec.Values)-1]
}

func applyJobStatus(r client.Reader, t *redskyv1alpha1.Trial, job *batchv1.Job, time *metav1.Time) (bool, bool) {
	var dirty bool

	// Get the interval of the container execution in the job pods
	startedAt := job.Status.StartTime
	finishedAt := job.Status.CompletionTime
	if matchingSelector, err := meta.MatchingSelector(job.Spec.Selector); err == nil {
		pods := &corev1.PodList{}
		if err := r.List(context.TODO(), pods, matchingSelector, client.InNamespace(job.Namespace)); err == nil {
			startedAt, finishedAt = containerTime(pods)

			// Check if the job has a start/completion time, but it is not yet reflected in the pod state we are seeing
			if (startedAt == nil && job.Status.StartTime != nil) || (finishedAt == nil && job.Status.CompletionTime != nil) {
				return false, true
			}
		}
	}

	// Adjust the trial start time
	if startTime, updated := latestTime(t.Status.StartTime, startedAt, t.Spec.StartTimeOffset); updated {
		t.Status.StartTime = startTime
		dirty = true
	}

	// Adjust the trial completion time
	if completionTime, updated := earliestTime(t.Status.CompletionTime, finishedAt); updated {
		t.Status.CompletionTime = completionTime
		dirty = true
	}

	// Mark the trial as failed if the job itself failed
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialFailed, corev1.ConditionTrue, c.Reason, c.Message, time)
			dirty = true
		}
	}

	return dirty, false
}

func containerTime(pods *corev1.PodList) (startedAt *metav1.Time, finishedAt *metav1.Time) {
	for i := range pods.Items {
		for j := range pods.Items[i].Status.ContainerStatuses {
			s := &pods.Items[i].Status.ContainerStatuses[j].State
			if s.Running != nil {
				startedAt, _ = earliestTime(startedAt, &s.Running.StartedAt)
			} else if s.Terminated != nil {
				startedAt, _ = earliestTime(startedAt, &s.Terminated.StartedAt)
				finishedAt, _ = latestTime(finishedAt, &s.Terminated.FinishedAt, nil)
			}
		}
	}
	return
}

func earliestTime(c, n *metav1.Time) (*metav1.Time, bool) {
	if n != nil && (c == nil || n.Before(c)) {
		return n.DeepCopy(), true
	}
	return c, false
}

func latestTime(c, n *metav1.Time, offset *metav1.Duration) (*metav1.Time, bool) {
	if n != nil && (c == nil || c.Before(n)) {
		if offset != nil {
			t := metav1.NewTime(n.Add(offset.Duration))
			return &t, true
		}
		return n.DeepCopy(), true
	}
	return c, false
}
