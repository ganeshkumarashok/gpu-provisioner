/*
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

package garbagecollection

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/lo"
	"go.uber.org/multierr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/clock"
	"knative.dev/pkg/logging"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	"github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/aws/karpenter-core/pkg/metrics"
	corecontroller "github.com/aws/karpenter-core/pkg/operator/controller"
	"github.com/aws/karpenter-core/pkg/utils/sets"
)

const (
	NodeHeartBeatAnnotationKey = "NodeHeartBeatTimeStamp"
)

type Controller struct {
	clock         clock.Clock
	kubeClient    client.Client
	cloudProvider cloudprovider.CloudProvider
}

func NewController(c clock.Clock, kubeClient client.Client, cloudProvider cloudprovider.CloudProvider) corecontroller.Controller {
	return &Controller{
		clock:         c,
		kubeClient:    kubeClient,
		cloudProvider: cloudProvider,
	}
}

func (c *Controller) Name() string {
	return "machine.garbagecollection"
}

// gpu-provisioner: leverage two perodic check to update machine readiness heartbeat
func (c *Controller) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	machineList := &v1alpha5.MachineList{}
	if err := c.kubeClient.List(ctx, machineList); err != nil {
		return reconcile.Result{}, err
	}

	// The NotReady nodes are excluded from the list
	cloudProviderMachines, err := c.cloudProvider.List(ctx)
	if err != nil {
		return reconcile.Result{}, err
	}
	cloudProviderMachines = lo.Filter(cloudProviderMachines, func(m *v1alpha5.Machine, _ int) bool {
		return m.DeletionTimestamp.IsZero()
	})
	cloudProviderProviderIDs := sets.New[string](lo.Map(cloudProviderMachines, func(m *v1alpha5.Machine, _ int) string {
		return m.Status.ProviderID
	})...)

	hbMachines := lo.Filter(lo.ToSlicePtr(machineList.Items), func(m *v1alpha5.Machine, _ int) bool {
		return m.StatusConditions().GetCondition(v1alpha5.MachineLaunched).IsTrue() &&
			m.DeletionTimestamp.IsZero()
	})

	var hbUpdated atomic.Uint64
	deletedMachines := []*v1alpha5.Machine{}
	// Update machine heartbeat,
	hbErrs := make([]error, len(hbMachines))
	workqueue.ParallelizeUntil(ctx, 20, len(hbMachines), func(i int) {
		stored := hbMachines[i].DeepCopy()
		updated := hbMachines[i].DeepCopy()

		if cloudProviderProviderIDs.Has(stored.Status.ProviderID) {
			hbUpdated.Add(1)
			if updated.Annotations == nil {
				updated.Annotations = make(map[string]string)
			}

			timeStr, _ := metav1.NewTime(time.Now()).MarshalJSON()
			updated.Annotations[NodeHeartBeatAnnotationKey] = string(timeStr)

			// If the machine was not ready, it becomes ready after getting the heartbeat.
			updated.StatusConditions().MarkTrue("Ready")
		} else {
			updated.StatusConditions().MarkFalse("Ready", "NodeNotReady", "Node status is NotReady")
		}
		statusCopy := updated.DeepCopy()
		updateCopy := updated.DeepCopy()
		if err := c.kubeClient.Patch(ctx, updated, client.MergeFrom(stored)); err != nil {
			hbErrs[i] = client.IgnoreNotFound(err)
			return
		}
		if err := c.kubeClient.Status().Patch(ctx, statusCopy, client.MergeFrom(stored)); err != nil {
			hbErrs[i] = client.IgnoreNotFound(err)
			return
		}
		if !updateCopy.StatusConditions().IsHappy() &&
			c.clock.Since(updateCopy.StatusConditions().GetCondition("Ready").LastTransitionTime.Inner.Time) > time.Minute*6 {
			deletedMachines = append(deletedMachines, updateCopy)
		}
	})
	logging.FromContext(ctx).Debugf(fmt.Sprintf("Update heartbeat for %d machines", hbUpdated.Load()))

	errs := make([]error, len(deletedMachines))
	workqueue.ParallelizeUntil(ctx, 20, len(deletedMachines), func(i int) {
		if err := c.kubeClient.Delete(ctx, deletedMachines[i]); err != nil {
			errs[i] = client.IgnoreNotFound(err)
			return
		}
		logging.FromContext(ctx).
			With("provisioner", deletedMachines[i].Labels[v1alpha5.ProvisionerNameLabelKey], "machine", deletedMachines[i].Name, "provider-id", deletedMachines[i].Status.ProviderID).
			Debugf("garbage collecting machine with no cloudprovider representation for more than 6 minutes")
		metrics.MachinesTerminatedCounter.With(prometheus.Labels{
			metrics.ReasonLabel:      "garbage_collected",
			metrics.ProvisionerLabel: deletedMachines[i].Labels[v1alpha5.ProvisionerNameLabelKey],
		}).Inc()
	})
	errs = append(errs, hbErrs...)
	return reconcile.Result{RequeueAfter: time.Minute * 2}, multierr.Combine(errs...)
}

func (c *Controller) Builder(_ context.Context, m manager.Manager) corecontroller.Builder {
	return corecontroller.NewSingletonManagedBy(m)
}
