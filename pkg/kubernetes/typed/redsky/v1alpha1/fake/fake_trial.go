// Code generated by client-gen. DO NOT EDIT.

package fake

import (
	v1alpha1 "github.com/gramLabs/redsky/pkg/apis/redsky/v1alpha1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	schema "k8s.io/apimachinery/pkg/runtime/schema"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeTrials implements TrialInterface
type FakeTrials struct {
	Fake *FakeRedskyV1alpha1
	ns   string
}

var trialsResource = schema.GroupVersionResource{Group: "redsky.carbonrelay.com", Version: "v1alpha1", Resource: "trials"}

var trialsKind = schema.GroupVersionKind{Group: "redsky.carbonrelay.com", Version: "v1alpha1", Kind: "Trial"}

// Get takes name of the trial, and returns the corresponding trial object, and an error if there is any.
func (c *FakeTrials) Get(name string, options v1.GetOptions) (result *v1alpha1.Trial, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(trialsResource, c.ns, name), &v1alpha1.Trial{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Trial), err
}

// List takes label and field selectors, and returns the list of Trials that match those selectors.
func (c *FakeTrials) List(opts v1.ListOptions) (result *v1alpha1.TrialList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(trialsResource, trialsKind, c.ns, opts), &v1alpha1.TrialList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1alpha1.TrialList{ListMeta: obj.(*v1alpha1.TrialList).ListMeta}
	for _, item := range obj.(*v1alpha1.TrialList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested trials.
func (c *FakeTrials) Watch(opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(trialsResource, c.ns, opts))

}

// Create takes the representation of a trial and creates it.  Returns the server's representation of the trial, and an error, if there is any.
func (c *FakeTrials) Create(trial *v1alpha1.Trial) (result *v1alpha1.Trial, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(trialsResource, c.ns, trial), &v1alpha1.Trial{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Trial), err
}

// Update takes the representation of a trial and updates it. Returns the server's representation of the trial, and an error, if there is any.
func (c *FakeTrials) Update(trial *v1alpha1.Trial) (result *v1alpha1.Trial, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(trialsResource, c.ns, trial), &v1alpha1.Trial{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Trial), err
}

// UpdateStatus was generated because the type contains a Status member.
// Add a +genclient:noStatus comment above the type to avoid generating UpdateStatus().
func (c *FakeTrials) UpdateStatus(trial *v1alpha1.Trial) (*v1alpha1.Trial, error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateSubresourceAction(trialsResource, "status", c.ns, trial), &v1alpha1.Trial{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Trial), err
}

// Delete takes name of the trial and deletes it. Returns an error if one occurs.
func (c *FakeTrials) Delete(name string, options *v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteAction(trialsResource, c.ns, name), &v1alpha1.Trial{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeTrials) DeleteCollection(options *v1.DeleteOptions, listOptions v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(trialsResource, c.ns, listOptions)

	_, err := c.Fake.Invokes(action, &v1alpha1.TrialList{})
	return err
}

// Patch applies the patch and returns the patched trial.
func (c *FakeTrials) Patch(name string, pt types.PatchType, data []byte, subresources ...string) (result *v1alpha1.Trial, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(trialsResource, c.ns, name, pt, data, subresources...), &v1alpha1.Trial{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1alpha1.Trial), err
}