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

// Package apis contains Kubernetes API groups.
package apis

import (
	_ "embed"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/aws/karpenter-core/pkg/operator/scheme"

	"github.com/samber/lo"

	coresettings "github.com/aws/karpenter-core/pkg/apis/settings"
	"github.com/azure/gpu-provisioner/pkg/apis/settings"
	"github.com/azure/gpu-provisioner/pkg/apis/v1alpha1"
)

var (
	// Builder includes all types within the apis package
	Builder = runtime.NewSchemeBuilder(
		v1alpha1.SchemeBuilder.AddToScheme,
	)
	// AddToScheme may be used to add all resources defined in the project to a Scheme
	AddToScheme = Builder.AddToScheme
	Settings    = []coresettings.Injectable{&settings.Settings{}}
)

//go:generate controller-gen crd object:headerFile="../../hack/boilerplate.go.txt" paths="./..." output:crd:artifacts:config=crds

func init() {
	lo.Must0(AddToScheme(scheme.Scheme))
}
