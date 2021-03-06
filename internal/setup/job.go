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

package setup

import (
	"encoding/base64"
	"fmt"
	"path"

	"github.com/redskyops/redskyops-controller/internal/template"
	"github.com/redskyops/redskyops-controller/internal/trial"
	redskyv1alpha1 "github.com/redskyops/redskyops-controller/pkg/apis/redsky/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// This is overwritten during builds to point to the actual image
var (
	// Image is the name of the setuptools image to use
	Image = "setuptools:latest"
	// ImagePullPolicy controls when the default image should be pulled
	ImagePullPolicy = string(corev1.PullIfNotPresent)
)

// NOTE: The default image names use a ":latest" tag which causes the default pull policy to switch
// from "IfNotPresent" to "Always". However, the default image names are not associated with a public
// repository and cannot actually be pulled (they only work if they are present). The exact opposite
// problem occurs with the production image names: we want those to have a policy of "Always" to address
// the potential of a floating tag but they will default to "IfNotPresent" because they do not use
// ":latest". To address this we always explicitly specify the pull policy corresponding to the image.
// Finally, when using digests, the default of "IfNotPresent" is acceptable as it is unambiguous.

// NewJob returns a new setup job for either create or delete
func NewJob(t *redskyv1alpha1.Trial, mode string) (*batchv1.Job, error) {
	job := &batchv1.Job{}
	job.Namespace = t.Namespace
	job.Name = fmt.Sprintf("%s-%s", t.Name, mode)
	job.Labels = map[string]string{
		redskyv1alpha1.LabelExperiment: t.ExperimentNamespacedName().Name,
		redskyv1alpha1.LabelTrial:      t.Name,
		redskyv1alpha1.LabelTrialRole:  "trialSetup",
	}
	job.Spec.BackoffLimit = new(int32)
	job.Spec.Template.Labels = map[string]string{
		redskyv1alpha1.LabelExperiment: t.ExperimentNamespacedName().Name,
		redskyv1alpha1.LabelTrial:      t.Name,
		redskyv1alpha1.LabelTrialRole:  "trialSetup",
	}
	job.Spec.Template.Spec.RestartPolicy = corev1.RestartPolicyNever
	job.Spec.Template.Spec.ServiceAccountName = t.Spec.SetupServiceAccountName

	// Collect the volumes we need for the pod
	var volumes = make(map[string]*corev1.Volume)
	for _, v := range t.Spec.SetupVolumes {
		volumes[v.Name] = &v
	}

	// We need to run as a non-root user that has the same UID and GID
	id := int64(1000)
	allowPrivilegeEscalation := false
	runAsNonRoot := true
	job.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
		RunAsNonRoot: &runAsNonRoot,
	}

	// Create containers for each of the setup tasks
	for _, task := range t.Spec.SetupTasks {
		if (mode == ModeCreate && task.SkipCreate) || (mode == ModeDelete && task.SkipDelete) {
			continue
		}
		c := corev1.Container{
			Name:  fmt.Sprintf("%s-%s", job.Name, task.Name),
			Image: task.Image,
			Args:  []string{mode},
			Env: []corev1.EnvVar{
				{Name: "NAMESPACE", Value: t.Namespace},
				{Name: "NAME", Value: task.Name},
				{Name: "TRIAL", Value: t.Name},
			},
			SecurityContext: &corev1.SecurityContext{
				RunAsUser:                &id,
				RunAsGroup:               &id,
				AllowPrivilegeEscalation: &allowPrivilegeEscalation,
			},
		}

		// Make sure we have an image
		if c.Image == "" {
			c.Image = Image
			c.ImagePullPolicy = corev1.PullPolicy(ImagePullPolicy)
		}

		// Add the trial assignments to the environment
		c.Env = trial.AppendAssignmentEnv(t, c.Env)

		// Add the configured volume mounts
		for _, vm := range task.VolumeMounts {
			c.VolumeMounts = append(c.VolumeMounts, vm)
		}

		// For Helm installs, serialize a Konjure configuration
		helmConfig := newHelmGeneratorConfig(&task)
		if helmConfig != nil {
			te := template.New()

			// Helm Values
			for _, hv := range task.HelmValues {
				hgv := helmGeneratorValue{
					Name:        hv.Name,
					ForceString: hv.ForceString,
				}

				if hv.ValueFrom != nil {
					// Evaluate the external value source
					switch {
					case hv.ValueFrom.ParameterRef != nil:
						v, ok := t.GetAssignment(hv.ValueFrom.ParameterRef.Name)
						if !ok {
							return nil, fmt.Errorf("invalid parameter reference '%s' for Helm value '%s'", hv.ValueFrom.ParameterRef.Name, hv.Name)
						}
						hgv.Value = v

					default:
						return nil, fmt.Errorf("unknown source for Helm value '%s'", hv.Name)
					}
				} else {
					// If there is no external source, evaluate the value field as a template
					v, err := te.RenderHelmValue(&hv, t)
					if err != nil {
						return nil, err
					}
					hgv.Value = v
				}

				helmConfig.Values = append(helmConfig.Values, hgv)
			}

			// Helm Values From
			for _, hvf := range task.HelmValuesFrom {
				if hvf.ConfigMap != nil {
					hgv := helmGeneratorValue{
						File: path.Join("/workspace", "helm-values", hvf.ConfigMap.Name, "*values.yaml"),
					}
					vm := corev1.VolumeMount{
						Name:      hvf.ConfigMap.Name,
						MountPath: path.Dir(hgv.File),
						ReadOnly:  true,
					}

					if _, ok := volumes[vm.Name]; !ok {
						vs := corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: hvf.ConfigMap.Name},
							},
						}
						volumes[vm.Name] = &corev1.Volume{Name: vm.Name, VolumeSource: vs}
					}
					c.VolumeMounts = append(c.VolumeMounts, vm)
					helmConfig.Values = append(helmConfig.Values, hgv)
				}
			}

			// Record the base64 encoded YAML representation in the environment
			b, err := yaml.Marshal(helmConfig)
			if err != nil {
				return nil, err
			}
			c.Env = append(c.Env, corev1.EnvVar{Name: "HELM_CONFIG", Value: base64.StdEncoding.EncodeToString(b)})
		}

		job.Spec.Template.Spec.Containers = append(job.Spec.Template.Spec.Containers, c)
	}

	// Add all of the volumes we collected to the pod
	for _, v := range volumes {
		job.Spec.Template.Spec.Volumes = append(job.Spec.Template.Spec.Volumes, *v)
	}

	return job, nil
}

type helmGeneratorValue struct {
	File        string      `json:"file,omitempty"`
	Name        string      `json:"name,omitempty"`
	Value       interface{} `json:"value,omitempty"`
	ForceString bool        `json:"forceString,omitempty"`
}

type helmGeneratorConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	ReleaseName       string               `json:"releaseName"`
	Chart             string               `json:"chart"`
	Version           string               `json:"version"`
	Values            []helmGeneratorValue `json:"values"`
}

func newHelmGeneratorConfig(task *redskyv1alpha1.SetupTask) *helmGeneratorConfig {
	if task.HelmChart == "" {
		return nil
	}

	cfg := &helmGeneratorConfig{
		ReleaseName: task.Name,
		Chart:       task.HelmChart,
		Version:     task.HelmChartVersion,
	}

	cfg.APIVersion = "konjure.carbonrelay.com/v1beta1"
	cfg.Kind = "HelmGenerator"
	cfg.Name = task.Name

	return cfg
}
