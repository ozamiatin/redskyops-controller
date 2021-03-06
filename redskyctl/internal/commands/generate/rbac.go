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

package generate

import (
	"path/filepath"
	"sort"
	"strings"

	redskyv1alpha1 "github.com/redskyops/redskyops-controller/pkg/apis/redsky/v1alpha1"
	"github.com/redskyops/redskyops-controller/redskyctl/internal/commander"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// TODO Determine if this should be exposed as a Kustomize plugin also
// TODO Instead of it's own plugin, have it be an option on the experiment plugin

type RBACOptions struct {
	// Printer is the resource printer used to render generated objects
	Printer commander.ResourcePrinter
	// IOStreams are used to access the standard process streams
	commander.IOStreams

	Filename     string
	Name         string
	IncludeNames bool

	mapper meta.RESTMapper
}

func NewRBACCommand(o *RBACOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rbac",
		Short: "Generate experiment roles",
		Long:  "Generate RBAC manifests from an experiment manifest",

		Annotations: map[string]string{
			commander.PrinterAllowedFormats: "json,yaml",
			commander.PrinterOutputFormat:   "yaml",
		},

		PreRun: func(cmd *cobra.Command, args []string) {
			commander.SetStreams(&o.IOStreams, cmd)
			o.Complete()
		},
		RunE: commander.WithoutArgsE(o.generate),
	}

	cmd.Flags().StringVarP(&o.Filename, "filename", "f", o.Filename, "File that contains the experiment to extract roles from.")
	cmd.Flags().StringVar(&o.Name, "role-name", o.Name, "Name of the cluster role to generate (default is to use a generated name).")
	cmd.Flags().BoolVar(&o.IncludeNames, "include-names", o.IncludeNames, "Include resource names in the generated role.")

	_ = cmd.MarkFlagFilename("filename", "yml", "yaml")

	commander.SetKubePrinter(&o.Printer, cmd)
	commander.ExitOnError(cmd)
	return cmd
}

func (o *RBACOptions) Complete() {
	// Create a REST mapper to convert from GroupVersionKind (used on patch targets) to GroupVersionResource (used in policy rules)
	rm := meta.NewDefaultRESTMapper(scheme.Scheme.PreferredVersionAllGroups())
	for gvk := range scheme.Scheme.AllKnownTypes() {
		rm.Add(gvk, meta.RESTScopeRoot)
	}
	o.mapper = rm
}

func (o *RBACOptions) generate() error {
	// Generate a cluster role
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   o.Name,
			Labels: map[string]string{"redskyops.dev/aggregate-to-patching": "true"},
		},
	}

	// Read the experiment
	// TODO When "readExperiment" returns multiple results, we still just generate a single cluster role
	experiment := &redskyv1alpha1.Experiment{}
	if err := readExperiment(o.Filename, o.In, experiment); err != nil {
		return err
	}

	// Come up with a default name if necessary
	if clusterRole.Name == "" {
		if experiment.Name != "" { // TODO Only take the experiment name if it is the only one `&& len(experiments) == 1`
			clusterRole.Name = "redsky-patching-" + strings.ReplaceAll(strings.ToLower(experiment.Name), " ", "-")
		} else if o.Filename == "-" {
			// TODO This needs some type of uniqueness
			clusterRole.Name = "redsky-patching-stdin"
		} else if o.Filename != "" {
			// TODO This needs more clean up
			clusterRole.Name = "redsky-patching-" + strings.ToLower(filepath.Base(o.Filename))
		} else {
			// TODO This probably doesn't work with cluster roles
			clusterRole.GenerateName = "redsky-patching-"
		}
	}

	// Add rules from the experiment
	rules, err := o.findRules(experiment)
	if err != nil {
		return err
	}
	for _, r := range rules {
		clusterRole.Rules = mergeRule(clusterRole.Rules, r)
	}

	// Do not generate an empty cluster role
	if len(clusterRole.Rules) == 0 {
		return nil
	}

	return o.Printer.PrintObj(clusterRole, o.Out)
}

// findRules finds the patch targets from an experiment
func (o *RBACOptions) findRules(exp *redskyv1alpha1.Experiment) ([]*rbacv1.PolicyRule, error) {
	var rules []*rbacv1.PolicyRule

	// Patches require "get" and "patch" permissions
	for i := range exp.Spec.Patches {
		// TODO This needs to use patch_controller.go `renderTemplate` to get the correct reference (e.g. SMP may have the ref in the payload)
		// NOTE: Technically we can not get the target reference without an actual trial; in most cases a dummy trial should work
		ref := exp.Spec.Patches[i].TargetRef
		if ref != nil {
			rules = append(rules, o.newPolicyRule(ref, "get", "patch"))
		}
	}

	// Readiness checks with no name require "list" permissions
	for i := range exp.Spec.Template.Spec.ReadinessChecks {
		ref := &exp.Spec.Template.Spec.ReadinessChecks[i].TargetRef
		if ref.Name == "" {
			rules = append(rules, o.newPolicyRule(ref, "list"))
		}
	}

	// Readiness gates will be converted to readiness checks; therefore we need the same check on non-empty names
	for i := range exp.Spec.Template.Spec.ReadinessGates {
		r := &exp.Spec.Template.Spec.ReadinessGates[i]
		if r.Name == "" {
			ref := &corev1.ObjectReference{Kind: r.Kind, APIVersion: r.APIVersion}
			rules = append(rules, o.newPolicyRule(ref, "list"))
		}
	}

	return rules, nil
}

// newPolicyRule creates a new policy rule for the specified object reference and list of verbs
func (o *RBACOptions) newPolicyRule(ref *corev1.ObjectReference, verbs ...string) *rbacv1.PolicyRule {
	// Start with the requested verbs
	r := &rbacv1.PolicyRule{
		Verbs: verbs,
	}

	// Get the mapping from GVK to GVR
	gvk := ref.GroupVersionKind()
	m, err := o.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		// TODO If this is guessing wrong too often we may need to allow additional mappings in the configuration
		m = &meta.RESTMapping{GroupVersionKind: gvk, Scope: meta.RESTScopeRoot}
		m.Resource, _ = meta.UnsafeGuessKindToResource(gvk)
	}
	r.APIGroups = []string{m.Resource.Group}
	r.Resources = []string{m.Resource.Resource}

	// Include the resource name if requested and available
	if o.IncludeNames && ref.Name != "" {
		r.ResourceNames = []string{ref.Name}
	}

	return r
}

// mergeRule attempts to combine the supplied rule with an existing compatible rule, failing that the rules are return with a new rule appended
func mergeRule(rules []rbacv1.PolicyRule, rule *rbacv1.PolicyRule) []rbacv1.PolicyRule {
	for i := range rules {
		r := &rules[i]
		if doesNotMatch(r.Verbs, rule.Verbs) {
			continue
		}
		if doesNotMatch(r.APIGroups, rule.APIGroups) {
			continue
		}
		if len(r.ResourceNames) > 0 && doesNotMatch(r.Resources, rule.Resources) {
			continue
		}

		for _, rr := range rule.Resources {
			r.Resources = appendMissing(r.Resources, rr)
		}
		sort.Strings(r.Resources)

		for _, rr := range rule.ResourceNames {
			r.ResourceNames = appendMissing(r.ResourceNames, rr)
		}
		sort.Strings(r.ResourceNames)

		return rules
	}
	return append(rules, *rule)
}

// doesNotMatch returns true if the two slices do not have the same ordered contents
func doesNotMatch(s1, s2 []string) bool {
	if len(s1) != len(s2) {
		return true
	}
	for i := range s1 {
		if s1[i] != s2[i] {
			return true
		}
	}
	return false
}

// appendMissing appends a string only if it does not already exist
func appendMissing(s []string, e string) []string {
	for _, i := range s {
		if i == e {
			return s
		}
	}
	return append(s, e)
}
