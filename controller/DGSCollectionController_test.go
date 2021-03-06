package controller

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/dgkanatsios/azuregameserversscalingkubernetes/shared"

	dgsv1alpha1 "github.com/dgkanatsios/azuregameserversscalingkubernetes/pkg/apis/azuregaming/v1alpha1"
	"github.com/dgkanatsios/azuregameserversscalingkubernetes/pkg/client/clientset/versioned/fake"
	dgsinformers "github.com/dgkanatsios/azuregameserversscalingkubernetes/pkg/client/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/diff"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	core "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
)

var (
	alwaysReady        = func() bool { return true }
	noResyncPeriodFunc = func() time.Duration { return 0 }
)

var podSpec = corev1.PodSpec{
	Containers: []corev1.Container{
		corev1.Container{
			Ports: []corev1.ContainerPort{}, // if we create any ports here we need to initialize port registry before (during??) running the test
		},
	},
}

type dgsColFixture struct {
	t *testing.T

	k8sClient *k8sfake.Clientset
	dgsClient *fake.Clientset
	// Objects to put in the store.
	dgsColLister []*dgsv1alpha1.DedicatedGameServerCollection
	dgsLister    []*dgsv1alpha1.DedicatedGameServer
	// Actions expected to happen on the client.
	dgsActions []core.Action
	// Objects from here preloaded into NewSimpleFake.
	k8sObjects []runtime.Object
	dgsObjects []runtime.Object
}

func newDGSColFixture(t *testing.T) *dgsColFixture {

	//stupid hack
	//currently, DGS names are generated randomly
	//however, we can't compare random names using deepEqual tests
	//so, we'll override the method that generates the names
	i := 0
	shared.GenerateRandomName = func(prefix string) string {
		i++
		return fmt.Sprintf("%s%d", prefix, i)
	}

	f := &dgsColFixture{}
	f.t = t
	f.dgsObjects = []runtime.Object{}
	return f
}

func (f *dgsColFixture) newDedicatedGameServerCollectionController() (*DedicatedGameServerCollectionController, dgsinformers.SharedInformerFactory) {
	f.k8sClient = k8sfake.NewSimpleClientset(f.k8sObjects...)
	f.dgsClient = fake.NewSimpleClientset(f.dgsObjects...)

	dgsInformers := dgsinformers.NewSharedInformerFactory(f.dgsClient, noResyncPeriodFunc())

	testController := NewDedicatedGameServerCollectionController(f.k8sClient, f.dgsClient,
		dgsInformers.Azuregaming().V1alpha1().DedicatedGameServerCollections(),
		dgsInformers.Azuregaming().V1alpha1().DedicatedGameServers())

	testController.dgsColListerSynced = alwaysReady
	testController.dgsListerSynced = alwaysReady
	testController.recorder = &record.FakeRecorder{}

	for _, dgsCol := range f.dgsColLister {
		dgsInformers.Azuregaming().V1alpha1().DedicatedGameServerCollections().Informer().GetIndexer().Add(dgsCol)
	}

	for _, dgs := range f.dgsLister {
		dgsInformers.Azuregaming().V1alpha1().DedicatedGameServers().Informer().GetIndexer().Add(dgs)
	}

	return testController, dgsInformers
}

func (f *dgsColFixture) run(dgsColName string) {
	f.runController(dgsColName, true, false)
}

func (f *dgsColFixture) runExpectError(dgsColName string) {
	f.runController(dgsColName, true, true)
}

func (f *dgsColFixture) runController(dgsColName string, startInformers bool, expectError bool) {
	testController, dgsInformers := f.newDedicatedGameServerCollectionController()
	if startInformers {
		stopCh := make(chan struct{})
		defer close(stopCh)
		dgsInformers.Start(stopCh)
	}

	err := testController.syncHandler(dgsColName)
	if !expectError && err != nil {
		f.t.Errorf("error syncing DGSCol: %v", err)
	} else if expectError && err == nil {
		f.t.Error("expected error syncing DGSCol, got nil")
	}

	//for this controller, we're getting only the actions on dgsClient
	actions := filterInformerActionsDGSCol(f.dgsClient.Actions())

	for i, action := range actions {
		if len(f.dgsActions) < i+1 {
			f.t.Errorf("%d unexpected actions: %+v", len(actions)-len(f.dgsActions), actions[i:])
			break
		}

		expectedAction := f.dgsActions[i]
		checkAction(expectedAction, action, f.t)
	}

	if len(f.dgsActions) > len(actions) {
		f.t.Errorf("%d additional expected actions:%+v", len(f.dgsActions)-len(actions), f.dgsActions[len(actions):])
	}
}

func (f *dgsColFixture) expectCreateDedicatedGameServerAction(d *dgsv1alpha1.DedicatedGameServer) {
	action := core.NewCreateAction(schema.GroupVersionResource{Resource: "dedicatedgameservers"}, d.Namespace, d)
	f.dgsActions = append(f.dgsActions, action)
}

func (f *dgsColFixture) expectUpdateDedicatedGameServerAction(d *dgsv1alpha1.DedicatedGameServer) {
	action := core.NewUpdateAction(schema.GroupVersionResource{Resource: "dedicatedgameservers"}, d.Namespace, d)
	f.dgsActions = append(f.dgsActions, action)
}

func (f *dgsColFixture) expectUpdateDedicatedGameServerCollectionAction(dgsCol *dgsv1alpha1.DedicatedGameServerCollection) {
	action := core.NewUpdateAction(schema.GroupVersionResource{Resource: "dedicatedgameservercollections"}, dgsCol.Namespace, dgsCol)
	f.dgsActions = append(f.dgsActions, action)
}

func getKeyDGSCol(dgsCol *dgsv1alpha1.DedicatedGameServerCollection, t *testing.T) string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(dgsCol)
	if err != nil {
		t.Errorf("Unexpected error getting key for DGSCol %v: %v", dgsCol.Name, err)
		return ""
	}
	return key
}

func TestCreatesDedicatedGameServerCollection(t *testing.T) {
	f := newDGSColFixture(t)

	dgsCol := shared.NewDedicatedGameServerCollection("test", shared.GameNamespace, 1, podSpec)

	f.dgsColLister = append(f.dgsColLister, dgsCol)
	f.dgsObjects = append(f.dgsObjects, dgsCol)

	expDGS := shared.NewDedicatedGameServer(dgsCol, "test1", podSpec)

	f.expectCreateDedicatedGameServerAction(expDGS)

	f.run(getKeyDGSCol(dgsCol, t))
}

func TestUpdateDedicatedGameServerCollectionStatus(t *testing.T) {
	f := newDGSColFixture(t)

	dgsCol := shared.NewDedicatedGameServerCollection("test", shared.GameNamespace, 1, podSpec)
	dgs := shared.NewDedicatedGameServer(dgsCol, "test1", podSpec)

	f.dgsColLister = append(f.dgsColLister, dgsCol)
	f.dgsLister = append(f.dgsLister, dgs)
	f.dgsObjects = append(f.dgsObjects, dgsCol)
	f.dgsObjects = append(f.dgsObjects, dgs)

	f.expectUpdateDedicatedGameServerCollectionAction(dgsCol)
	f.run(getKeyDGSCol(dgsCol, t))
}

func TestIncreaseReplicasOnDedicatedGameServerCollection(t *testing.T) {
	f := newDGSColFixture(t)

	dgsCol := shared.NewDedicatedGameServerCollection("test", shared.GameNamespace, 1, podSpec)
	dgs := shared.NewDedicatedGameServer(dgsCol, "test0", podSpec)

	f.dgsColLister = append(f.dgsColLister, dgsCol)
	f.dgsLister = append(f.dgsLister, dgs)
	f.dgsObjects = append(f.dgsObjects, dgsCol)
	f.dgsObjects = append(f.dgsObjects, dgs)

	//Update replicas
	dgsCol.Spec.Replicas = 2
	dgsExpected := shared.NewDedicatedGameServer(dgsCol, "test1", podSpec)

	f.expectCreateDedicatedGameServerAction(dgsExpected)
	f.run(getKeyDGSCol(dgsCol, t))
}

func TestDecreaseReplicasOnDedicatedGameServerCollection(t *testing.T) {
	f := newDGSColFixture(t)

	dgsCol := shared.NewDedicatedGameServerCollection("test", shared.GameNamespace, 1, podSpec)

	dgsCol.Status.DedicatedGameServerCollectionState = dgsv1alpha1.DedicatedGameServerCollectionStateRunning
	dgsCol.Status.PodCollectionState = corev1.PodRunning

	dgs := shared.NewDedicatedGameServerWithNoParent(dgsCol.Namespace, "test0", podSpec)

	f.dgsColLister = append(f.dgsColLister, dgsCol)
	f.dgsLister = append(f.dgsLister, dgs)
	f.dgsObjects = append(f.dgsObjects, dgsCol)
	f.dgsObjects = append(f.dgsObjects, dgs)

	//Update replicas
	dgsCol.Spec.Replicas = 0

	f.expectUpdateDedicatedGameServerCollectionAction(dgsCol)
	f.run(getKeyDGSCol(dgsCol, t))
}

// filterInformerActionsDGSCol filters list and watch actions for testing resources.
// Since list and watch don't change resource state we can filter it to lower
// noise level in our tests.
func filterInformerActionsDGSCol(actions []core.Action) []core.Action {
	ret := []core.Action{}
	for _, action := range actions {
		if len(action.GetNamespace()) == 0 &&
			(action.Matches("list", "dedicatedgameservercollections") ||
				action.Matches("watch", "dedicatedgameservercollections") ||
				action.Matches("list", "dedicatedgameservers") ||
				action.Matches("watch", "dedicatedgameservers")) {
			continue
		}
		ret = append(ret, action)
	}

	return ret
}

// checkAction verifies that expected and actual actions are equal and both have
// same attached resources
func checkAction(expected, actual core.Action, t *testing.T) {
	if !(expected.Matches(actual.GetVerb(), actual.GetResource().Resource) && actual.GetSubresource() == expected.GetSubresource()) {
		t.Errorf("Expected\n\t%#v\ngot\n\t%#v", expected, actual)
		return
	}

	if reflect.TypeOf(actual) != reflect.TypeOf(expected) {
		t.Errorf("Action has wrong type. Expected: %t. Got: %t", expected, actual)
		return
	}

	switch a := actual.(type) {
	case core.CreateAction:
		e, _ := expected.(core.CreateAction)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expObject, object))
		}
	case core.UpdateAction:
		e, _ := expected.(core.UpdateAction)
		expObject := e.GetObject()
		object := a.GetObject()

		if !reflect.DeepEqual(expObject, object) {
			t.Errorf("Action %s %s has wrong object\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expObject, object))
		}
	case core.PatchAction:
		e, _ := expected.(core.PatchAction)
		expPatch := e.GetPatch()
		patch := a.GetPatch()

		if !reflect.DeepEqual(expPatch, expPatch) {
			t.Errorf("Action %s %s has wrong patch\nDiff:\n %s",
				a.GetVerb(), a.GetResource().Resource, diff.ObjectGoPrintDiff(expPatch, patch))
		}
	}
}
