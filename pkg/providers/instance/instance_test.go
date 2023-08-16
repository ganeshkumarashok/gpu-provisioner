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

package instance

import (
	"testing"

	"github.com/aws/karpenter-core/pkg/apis/v1alpha5"
	"github.com/aws/karpenter-core/pkg/cloudprovider"
	"github.com/gpu-vmprovisioner/pkg/apis/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func TestGetPriorityCapacityAndInstanceType(t *testing.T) {
	cases := []struct {
		name                 string
		instanceTypes        []*cloudprovider.InstanceType
		machine              *v1alpha5.Machine
		expectedInstanceType string
		expectedPriority     string
		expectedZone         string
	}{
		{
			name:                 "No instance types in the list",
			instanceTypes:        []*cloudprovider.InstanceType{},
			machine:              &v1alpha5.Machine{},
			expectedInstanceType: "",
			expectedPriority:     "",
			expectedZone:         "",
		},
		{
			name: "Selects First, Cheapest SKU",
			instanceTypes: []*cloudprovider.InstanceType{
				{
					Name: "Standard_D2s_v3",
					Offerings: []cloudprovider.Offering{
						{
							Price:        0.1,
							Zone:         "westus-2",
							CapacityType: v1alpha1.PriorityRegular,
							Available:    true,
						},
					},
				},
				{
					Name: "Standard_NV16as_v4",
					Offerings: []cloudprovider.Offering{
						{
							Price:        0.1,
							Zone:         "westus-2",
							CapacityType: v1alpha1.PriorityRegular,
							Available:    true,
						},
					},
				},
			},
			machine:              &v1alpha5.Machine{},
			expectedInstanceType: "Standard_D2s_v3",
			expectedZone:         "2",
			expectedPriority:     v1alpha1.PriorityRegular,
		},
	}
	provider := NewProvider(nil, nil, nil,
		"westus-2",
		"MC_xxxxx_yyyy-region",
		"/subscriptions/0000000-0000-0000-0000-0000000000/resourceGroups/fake-resource-group-name/providers/Microsoft.Network/virtualNetworks/karpenter/subnets/nodesubnet",
		"cluster-name")
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			instanceType := c.instanceTypes[0]
			priority := provider.getPriorityForInstanceType(c.machine, instanceType)
			if instanceType != nil {
				assert.Equal(t, c.expectedInstanceType, instanceType.Name)
			}
			assert.Equal(t, c.expectedPriority, priority)
		})
	}
}
