/*
Copyright 2020 GramLabs, Inc.

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

package experiments

import (
	"context"
	"fmt"
	"sort"

	experimentsv1alpha1 "github.com/redskyops/redskyops-controller/redskyapi/experiments/v1alpha1"
	"github.com/redskyops/redskyops-controller/redskyctl/internal/commander"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/labels"
)

// GetOptions includes the configuration for getting experiment API objects
type GetOptions struct {
	Options

	ChunkSize int
	SortBy    string
	Selector  string

	meta experimentsMeta
}

// NewGetCommand creates a new get command
func NewGetCommand(o *GetOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get",
		Short: "Display a Red Sky resource",
		Long:  "Get Red Sky resources from the remote server",

		PreRunE: func(cmd *cobra.Command, args []string) error {
			commander.SetStreams(&o.IOStreams, cmd)
			return commander.SetExperimentsAPI(&o.ExperimentsAPI, o.Config, cmd)
		},
		RunE: commander.WithContextE(o.get),
	}

	cmd.Flags().IntVar(&o.ChunkSize, "chunk-size", o.ChunkSize, "Fetch large lists in chunks rather then all at once.")
	cmd.Flags().StringVarP(&o.Selector, "selector", "l", o.Selector, "Selector (label `query`) to filter on.")
	cmd.Flags().StringVar(&o.SortBy, "sort-by", o.SortBy, "Sort list types using this JSONPath `expression`.")

	TypeAndNameArgs(cmd, &o.Options)
	commander.SetPrinter(&o.meta, &o.Printer, cmd)
	commander.ExitOnError(cmd)
	return cmd
}

func (o *GetOptions) get(ctx context.Context) error {
	switch o.GetType() {
	case TypeExperiment:
		if err := o.getExperiment(ctx); err != nil {
			return err
		}
	case TypeExperimentList:
		if err := o.getExperimentList(ctx); err != nil {
			return err
		}
	case TypeTrialList:
		for _, name := range o.Names {
			if err := o.getTrialList(ctx, name); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("cannot get %s", o.GetType())
	}
	return nil
}

func (o *GetOptions) getExperiment(ctx context.Context) error {
	// Create a list to hold the experiments
	l := &experimentsv1alpha1.ExperimentList{}
	for _, n := range o.Names {
		exp, err := o.ExperimentsAPI.GetExperimentByName(ctx, experimentsv1alpha1.NewExperimentName(n))
		if err != nil {
			return err
		}
		l.Experiments = append(l.Experiments, experimentsv1alpha1.ExperimentItem{Experiment: exp})
	}

	// If this was a request for a single object, just print it out (e.g. don't produce a JSON list for a single element)
	if len(o.Names) == 1 && len(l.Experiments) == 1 {
		return o.Printer.PrintObj(&l.Experiments[0], o.Out)
	}

	if err := o.filterAndSortExperiments(l); err != nil {
		return err
	}

	return o.Printer.PrintObj(&l, o.Out)
}

func (o *GetOptions) getExperimentList(ctx context.Context) error {
	if len(o.Names) > 0 {
		// TODO Is there a better place to enforce this?
		return fmt.Errorf("cannot specify names with experiment list")
	}

	// Get all the experiments one page at a time
	l, err := o.ExperimentsAPI.GetAllExperiments(ctx, &experimentsv1alpha1.ExperimentListQuery{Limit: o.ChunkSize})
	if err != nil {
		return err
	}

	n := l
	for n.Next != "" {
		if n, err = o.ExperimentsAPI.GetAllExperimentsByPage(ctx, n.Next); err != nil {
			return err
		}
		l.Experiments = append(l.Experiments, n.Experiments...)
	}

	if err := o.filterAndSortExperiments(&l); err != nil {
		return err
	}

	return o.Printer.PrintObj(&l, o.Out)
}

func (o *GetOptions) filterAndSortExperiments(l *experimentsv1alpha1.ExperimentList) error {
	// Experiments do not have labels so anything but the empty selector will just nil out the list
	if sel, err := labels.Parse(o.Selector); err != nil {
		return err
	} else if !sel.Empty() {
		l.Experiments = nil
	}

	// If sorting was requested, sort using maps with all the sortable keys
	if o.SortBy != "" {
		sort.Slice(l.Experiments, sortByField(o.SortBy, func(i int) interface{} { return sortableExperimentData(&l.Experiments[i]) }))
	}

	return nil
}

func (o *GetOptions) getTrialList(ctx context.Context, name string) error {
	// Get the experiment
	exp, err := o.ExperimentsAPI.GetExperimentByName(context.TODO(), experimentsv1alpha1.NewExperimentName(name))
	if err != nil {
		return err
	}

	// Store the experiment in metadata
	o.meta.base = &exp

	// Fetch the trial data
	q := &experimentsv1alpha1.TrialListQuery{Status: []experimentsv1alpha1.TrialStatus{
		experimentsv1alpha1.TrialActive,
		experimentsv1alpha1.TrialCompleted,
		experimentsv1alpha1.TrialFailed,
	}}
	var l experimentsv1alpha1.TrialList
	if exp.Trials != "" {
		l, err = o.ExperimentsAPI.GetAllTrials(ctx, exp.Trials, q)
		if err != nil {
			return err
		}
	}

	// Filter the trial list using Kubernetes label selectors
	if sel, err := labels.Parse(o.Selector); err != nil {
		return err
	} else if !sel.Empty() {
		var filtered []experimentsv1alpha1.TrialItem
		for i := range l.Trials {
			// TODO Add status into the label map?
			if sel.Matches(labels.Set(l.Trials[i].Labels)) {
				filtered = append(filtered, l.Trials[i])
			}
		}
		l.Trials = filtered
	}

	// If sorting was requested, sort using maps with all the sortable keys
	if o.SortBy != "" {
		sort.Slice(l.Trials, sortByField(o.SortBy, func(i int) interface{} { return sortableTrialData(&l.Trials[i]) }))
	}

	return o.Printer.PrintObj(&l, o.Out)
}

// sortableExperimentData slightly modifies the schema of the experiment item to make it easier to specify sort orders
func sortableExperimentData(item *experimentsv1alpha1.ExperimentItem) map[string]interface{} {
	d := make(map[string]interface{}, 1)
	d["name"] = item.DisplayName
	return d
}

// sortableTrialData slightly modifies the schema of the trial item to make it easier to specify sort orders
func sortableTrialData(item *experimentsv1alpha1.TrialItem) map[string]interface{} {
	assignments := make(map[string]int64, len(item.Assignments))
	for i := range item.Assignments {
		if a, err := item.Assignments[i].Value.Int64(); err == nil {
			assignments[item.Assignments[i].ParameterName] = a
		}
	}

	values := make(map[string]interface{}, len(item.Values))
	for i := range item.Values {
		v := make(map[string]float64, 2)
		v["value"] = item.Values[i].Value
		v["error"] = item.Values[i].Error
		values[item.Values[i].MetricName] = v
	}

	d := make(map[string]interface{}, 5)
	d["assignments"] = assignments
	d["labels"] = item.Labels
	d["number"] = item.Number
	d["status"] = item.Status
	d["values"] = values
	return d
}
