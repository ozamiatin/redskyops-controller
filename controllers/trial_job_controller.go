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
	"time"

	"github.com/go-logr/logr"
	"github.com/redskyops/k8s-experiment/internal/controller"
	"github.com/redskyops/k8s-experiment/internal/meta"
	"github.com/redskyops/k8s-experiment/internal/trial"
	redskyv1alpha1 "github.com/redskyops/k8s-experiment/pkg/apis/redsky/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// TrialJobReconciler reconciles a Trial's job
type TrialJobReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=redskyops.dev,resources=trials,verbs=get;list;watch;update
// +kubebuilder:rbac:groups=batch;extensions,resources=jobs,verbs=list;watch;create
// +kubebuilder:rbac:groups="",resources=pods,verbs=list

func (r *TrialJobReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	now := metav1.Now()
	t := &redskyv1alpha1.Trial{}
	if err := r.Get(ctx, req.NamespacedName, t); err != nil || r.ignoreTrial(t) {
		return ctrl.Result{}, controller.IgnoreNotFound(err)
	}

	if result, err := r.runTrialJob(ctx, t, &now); result != nil {
		return *result, err
	}

	return ctrl.Result{}, nil
}

func (r *TrialJobReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("trial-job").
		For(&redskyv1alpha1.Trial{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

func (r *TrialJobReconciler) ignoreTrial(t *redskyv1alpha1.Trial) bool {
	// Ignore deleted trials
	if !t.DeletionTimestamp.IsZero() {
		return true
	}

	// Ignore failed trials
	if trial.CheckCondition(&t.Status, redskyv1alpha1.TrialFailed, corev1.ConditionTrue) {
		return true
	}

	// Ignore trials that already have a start and completion time
	if t.Status.StartTime != nil && t.Status.CompletionTime != nil {
		return true
	}

	// Ignore trials that have an initializer
	if t.HasInitializer() {
		return true
	}

	// Ignore trials whose patches have not been evaluated yet
	// NOTE: If "trial patched" is not unknown, then `len(t.Spec.PatchOperations) == 0` means the experiment had no patches
	if trial.CheckCondition(&t.Status, redskyv1alpha1.TrialPatched, corev1.ConditionUnknown) {
		return true
	}

	// Ignore trials that have not stabilized
	for i := range t.Spec.PatchOperations {
		if t.Spec.PatchOperations[i].Wait {
			return true
		}
	}

	return false
}

func (r *TrialJobReconciler) runTrialJob(ctx context.Context, t *redskyv1alpha1.Trial, probeTime *metav1.Time) (*ctrl.Result, error) {
	// TODO Remove this once we know why it is actually needed (if it is needed)
	for _, c := range t.Status.Conditions {
		if c.Type == redskyv1alpha1.TrialStable && c.LastTransitionTime.Add(1*time.Second).After(probeTime.Time) {
			return &ctrl.Result{RequeueAfter: 1 * time.Second}, nil
		}
	}

	// List the trial jobs; really, there should only ever be 0 or 1 matching jobs
	jobList := &batchv1.JobList{}
	if err := r.listJobs(ctx, jobList, t); err != nil {
		return &ctrl.Result{}, err
	}

	if len(jobList.Items) == 0 {
		job := trial.NewJob(t)
		if err := controllerutil.SetControllerReference(t, job, r.Scheme); err != nil {
			return &ctrl.Result{}, err
		}

		err := r.Create(ctx, job)
		return &ctrl.Result{}, err
	}

	for i := range jobList.Items {
		if update, requeue := r.applyJobStatus(ctx, t, &jobList.Items[i], probeTime); update {
			err := r.Update(ctx, t)
			return controller.RequeueConflict(err)
		} else if requeue {
			// We are watching jobs, not pods; we may need to poll the pod state before it is consistent
			return &ctrl.Result{Requeue: true}, nil
		}
	}

	return nil, nil
}

// listJobs will return all of the jobs for the trial
func (r *TrialJobReconciler) listJobs(ctx context.Context, jobList *batchv1.JobList, t *redskyv1alpha1.Trial) error {
	matchingSelector, err := meta.MatchingSelector(t.GetJobSelector())
	if err != nil {
		return err
	}
	if err := r.List(ctx, jobList, matchingSelector); err != nil {
		return err
	}

	// Setup jobs always have "role=trialSetup" so ignore jobs with that label
	// NOTE: We do not use label selectors on search because we don't know if they are user modified
	items := jobList.Items[:0]
	for i := range jobList.Items {
		if jobList.Items[i].Labels[redskyv1alpha1.LabelTrialRole] != "trialSetup" {
			items = append(items, jobList.Items[i])
		}
	}
	jobList.Items = items

	return nil
}

func (r *TrialJobReconciler) applyJobStatus(ctx context.Context, t *redskyv1alpha1.Trial, job *batchv1.Job, time *metav1.Time) (bool, bool) {
	var dirty bool

	// Get the interval of the container execution in the job pods
	startedAt := job.Status.StartTime
	finishedAt := job.Status.CompletionTime
	if matchingSelector, err := meta.MatchingSelector(job.Spec.Selector); err == nil {
		podList := &corev1.PodList{}
		if err := r.List(ctx, podList, client.InNamespace(job.Namespace), matchingSelector); err == nil {
			startedAt, finishedAt = containerTime(podList)

			// Check if the job has a start/completion time, but it is not yet reflected in the pod state we are seeing
			if (startedAt == nil && job.Status.StartTime != nil) || (finishedAt == nil && job.Status.CompletionTime != nil) {
				return false, true
			}

			// Look for pod failures (edge case where job controller doesn't update status properly, e.g. initContainer failure)
			for i := range podList.Items {
				s := &podList.Items[i].Status
				if s.Phase == corev1.PodFailed {
					trial.ApplyCondition(&t.Status, redskyv1alpha1.TrialFailed, corev1.ConditionTrue, s.Reason, "", time)
					dirty = true
				}
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
