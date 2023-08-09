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

package bootstrap

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"fmt"

	"strings"
	"text/template"

	"github.com/Masterminds/semver"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
)

type AKS struct {
	Options

	// more values needed for AKS bootstrap (the need for some of these may go away in the future)
	TenantID                       string
	SubscriptionID                 string
	UserAssignedIdentityID         string
	Location                       string
	ResourceGroup                  string
	ClusterID                      string
	APIServerName                  string
	KubeletClientTLSBootstrapToken string
	NetworkPlugin                  string
	NetworkPolicy                  string
	KubernetesVersion              string
}

var _ Bootstrapper = (*AKS)(nil) // assert AKS implements Bootstrapper

func (a AKS) Script() (string, error) {
	bootstrapScript, err := a.aksBootstrapScript()
	if err != nil {
		return "", fmt.Errorf("error getting AKS bootstrap script: %w", err)
	}

	// fmt.Print(bootstrapScript)
	return base64.StdEncoding.EncodeToString([]byte(bootstrapScript)), nil
}

// Config item types classified by code:
//
// - : known unnecessary or unused - (empty) value set in code, until dropped from template
// n : not (yet?) supported, set to empty or something reasonable in code
// s : static/constant (or very slow changing), value set in code;
//     also the choice for something that does not have to be exposed for customization yet
//
// a : known argument/parameter, passed in (usually from environment)
// x : unique per cluster,  extracted or specified. (Candidates for exposure/accessibility via API)
// X : unique per nodepool, extracted or specified. (Candidates for exposure/accessibility via API)
// c : user input, global Karpenter config (provider-specific)
// p : user input, part of standard Provisioner (NodePool) CR spec. Example: custom labels, kubelet config
// t : user input, NodeTemplate (potentially per node)
// k : computed (at runtime) by Karpenter (e.g. based on VM SKU, extra labels, etc.)
//     (xk - computed from per cluster data, such as cluster id)
//
// ? : needs more investigation
//
// multiple codes: combined from several sources

// Config sources for types:
//
// Hardcoded (this file)                         : unused (-), static (s) and unsupported (n), as well as selected defaults (s)
// Computed at runtime                           : computed (k)
// Karpenter global ConfigMap (provider-specific): cluster-level user input (c) - ALL DEFAULTED FOR NOW
//                                               : as well as unique per cluster (x) - until we have a better place for these
// (TBD)                                         : unique per nodepool. extracted or specified (X)
// NodeTemplate                                  : user input that could be per-node (t) - ALL DEFAULTED FOR NOW
// Provisioner spec                              : selected nodepool-level user input (p)

// NodeBootstrapVariables carries all variables needed to bootstrap a node
// It is used as input rendering the bootstrap script Go template (customDataTemplate)
type NodeBootstrapVariables struct {
	IsAKSCustomCloud                  bool     // n   (false)
	InitAKSCustomCloudFilepath        string   // n   (static)
	AKSCustomCloudRepoDepotEndpoint   string   // n   derived from custom cloud env?
	AdminUsername                     string   // t   typically azureuser but can be user input
	MobyVersion                       string   // -   unnecessary
	TenantID                          string   // p   environment derived, unnecessary?
	KubernetesVersion                 string   // ?   cluster/node pool specific, derived from user input
	HyperkubeURL                      string   // -   should be unnecessary
	KubeBinaryURL                     string   // -   necessary only for non-cached versions / static-ish
	CustomKubeBinaryURL               string   // -   unnecessary
	KubeproxyURL                      string   // -   should be unnecessary or bug
	APIServerPublicKey                string   // -   unique per cluster, actually not sure best way to extract? [should not be needed on agent nodes]
	SubscriptionID                    string   // a   can be derived from environment/imds
	ResourceGroup                     string   // a   can be derived from environment/imds
	Location                          string   // a   can be derived from environment/imds
	VMType                            string   // xd  derived from cluster but unnecessary (?) only used by CCM [will default to "vmss" for now]
	Subnet                            string   // xd  derived from cluster but unnecessary (?) only used by CCM [will default to "aks-subnet for now]
	NetworkSecurityGroup              string   // xk  derived from cluster but unnecessary (?) only used by CCM [= "aks-agentpool-<clusterid>-nsg" for now]
	VirtualNetwork                    string   // xk  derived from cluster but unnecessary (?) only used by CCM [= "aks-vnet-<clusterid>" for now]
	VirtualNetworkResourceGroup       string   // xd  derived from cluster but unnecessary (?) only used by CCM [default to empty, looks like unused]
	RouteTable                        string   // xk  derived from cluster but unnecessary (?) only used by CCM [= "aks-agentpool-<clusterid>-routetable" for now]
	PrimaryAvailabilitySet            string   // -   derived from cluster but unnecessary (?) only used by CCM
	PrimaryScaleSet                   string   // -   derived from cluster but unnecessary (?) only used by CCM
	ServicePrincipalClientID          string   // ad  user input
	NetworkPlugin                     string   // x   user input (? actually derived from cluster, right?)
	NetworkPolicy                     string   // x   user input / unique per cluster. user-specified.
	VNETCNILinuxPluginsURL            string   // -   unnecessary [actually, currently required]
	CNIPluginsURL                     string   // -   unnecessary [actually, currently required]
	CloudProviderBackoff              bool     // s   BEGIN CLOUD CONFIG for azure stuff, static/derived from user inputs
	CloudProviderBackoffMode          string   // s   [static until has to be exposed; could propagate Karpenter RL config, but won't]
	CloudProviderBackoffRetries       string   // s
	CloudProviderBackoffExponent      string   // s
	CloudProviderBackoffDuration      string   // s
	CloudProviderBackoffJitter        string   // s
	CloudProviderRatelimit            bool     // s
	CloudProviderRatelimitQPS         string   // s
	CloudProviderRatelimitQPSWrite    string   // s
	CloudProviderRatelimitBucket      string   // s
	CloudProviderRatelimitBucketWrite string   // s
	LoadBalancerDisableOutboundSNAT   bool     // xd  [= false for now]
	UseManagedIdentityExtension       bool     // s   [always false?]
	UseInstanceMetadata               bool     // s   [always true?]
	LoadBalancerSKU                   string   // xd  [= "Standard" for now]
	ExcludeMasterFromStandardLB       bool     // s   [always true?]
	MaximumLoadbalancerRuleCount      int      // xd  END CLOUD CONFIG [will default to 250 for now]
	ContainerRuntime                  string   // s   always containerd
	CLITool                           string   // s   static/unnecessary
	ContainerdDownloadURLBase         string   // -   unnecessary
	NetworkMode                       string   // c   user input
	UserAssignedIdentityID            string   // a   user input
	APIServerName                     string   // x   unique per cluster
	IsVHD                             bool     // s   static-ish
	GPUNode                           bool     // k   derived from VM size
	SGXNode                           bool     // -   unused
	MIGNode                           bool     // t   user input
	ConfigGPUDriverIfNeeded           bool     // s   depends on hardware, unnecessary for oss, but aks provisions gpu drivers
	EnableGPUDevicePluginIfNeeded     bool     // -   deprecated/preview only, don't do this for OSS
	TeleportdPluginDownloadURL        string   // -   user input, don't do this for OSS
	ContainerdVersion                 string   // -   unused
	ContainerdPackageURL              string   // -   only for testing
	RuncVersion                       string   // -   unused
	RuncPackageURL                    string   // -   testing only
	EnableHostsConfigAgent            bool     // n   derived from private cluster user input...I think?
	DisableSSH                        bool     // t   user input
	NeedsContainerd                   bool     // s   static true
	TeleportEnabled                   bool     // t   user input
	ShouldConfigureHTTPProxy          bool     // c   user input
	ShouldConfigureHTTPProxyCA        bool     // c   user input [secret]
	HTTPProxyTrustedCA                string   // c   user input [secret]
	ShouldConfigureCustomCATrust      bool     // c   user input
	CustomCATrustConfigCerts          []string // c   user input [secret]
	IsKrustlet                        bool     // t   user input
	GPUNeedsFabricManager             bool     // v   determined by GPU hardware type
	NeedsDockerLogin                  bool     // t   user input [still needed?]
	IPv6DualStackEnabled              bool     // t   user input
	OutboundCommand                   string   // s   mostly static/can be
	EnableUnattendedUpgrades          bool     // c   user input [presumably cluster level, correct?]
	EnsureNoDupePromiscuousBridge     bool     // k   derived {{ and NeedsContainerd IsKubenet (not HasCalicoNetworkPolicy) }} [could be computed by template ...]
	ShouldConfigSwapFile              bool     // t   user input
	ShouldConfigTransparentHugePage   bool     // t   user input
	TargetCloud                       string   // n   derive from environment/user input
	TargetEnvironment                 string   // n   derive from environment/user input
	CustomEnvJSON                     string   // n   derive from environment/user input
	IsCustomCloud                     bool     // n   derive from environment/user input
	CSEHelpersFilepath                string   // s   static
	CSEDistroHelpersFilepath          string   // s   static
	CSEInstallFilepath                string   // s   static
	CSEDistroInstallFilepath          string   // s   static
	CSEConfigFilepath                 string   // s   static
	AzurePrivateRegistryServer        string   // c   user input
	HasCustomSearchDomain             bool     // c   user input
	CustomSearchDomainFilepath        string   // s   static
	HTTPProxyURLs                     string   // c   user input [presumably cluster-level]
	HTTPSProxyURLs                    string   // c   user input [presumably cluster-level]
	NoProxyURLs                       string   // c   user input [presumably cluster-level]
	ClientTLSBootstrappingEnabled     bool     // s   static true
	DHCPv6ServiceFilepath             string   // k   derived from user input [how?]
	DHCPv6ConfigFilepath              string   // k   derived from user input [how?]
	THPEnabled                        string   // c   user input [presumably cluster-level][should be bool?]
	THPDefrag                         string   // c   user input [presumably cluster-level][should be bool?]
	ServicePrincipalFileContent       string   // s   only required for RP cluster [static: msi?]
	KubeletClientContent              string   // -   unnecessary [if using TLS bootstrapping]
	KubeletClientCertContent          string   // -   unnecessary
	KubeletConfigFileEnabled          bool     // s   can be static	[should kubelet config be actually used/preferred instead of flags?]
	KubeletConfigFileContent          string   // s   mix of user/static/RP-generated.
	SwapFileSizeMB                    int      // t   user input
	GPUDriverVersion                  string   // k   determine by OS + GPU hardware requirements; can be determined automatically, but hard. suggest using GPU operator.
	GPUInstanceProfile                string   // t   user-specified
	CustomSearchDomainName            string   // c   user-specified [presumably cluster-level]
	CustomSearchRealmUser             string   // c   user-specified [presumably cluster-level]
	CustomSearchRealmPassword         string   // c   user-specified [presumably cluster-level]
	MessageOfTheDay                   string   // t   user-specified [presumably node-level]
	HasKubeletDiskType                bool     // t   user-specified [presumably node-level]
	NeedsCgroupsv2                    bool     // k   can be automatically determined
	SysctlContent                     string   // t   user-specified
	TLSBootstrapToken                 string   // X   nodepool or node specific. can be created automatically
	KubeletFlags                      string   // psX unique per nodepool. partially user-specified, static, and RP-generated
	KubeletNodeLabels                 string   // pk  node-pool specific. user-specified.
	AzureEnvironmentFilepath          string   // s   can be made static [usually "/etc/kubernetes/azure.json", but my examples use ""?]
	KubeCACrt                         string   // x   unique per cluster
	KubenetTemplate                   string   // s   static
	ContainerdConfigContent           string   // k   determined by GPU VM size, WASM support, Kata support
	IsKata                            bool     // n   user-specified
}

var (
	//go:embed cse_cmd.sh.gtpl
	customDataTemplateText string
	customDataTemplate     = template.Must(template.New("customdata").Parse(customDataTemplateText))

	//go:embed sysctl.conf
	sysctlContent []byte
	//go:embed kubenet-cni.json.gtpl
	kubenetTemplate []byte
	//go:embed containerd.toml
	containerdConfigContent []byte

	// source note: unique per nodepool. partially user-specified, static, and RP-generated
	// removed --image-pull-progress-deadline=30m  (not in 1.24?)
	// removed --network-plugin=cni (not in 1.24?)
	kubeletFlagsBase = map[string]string{
		"--address":                           "0.0.0.0",
		"--anonymous-auth":                    "false",
		"--authentication-token-webhook":      "true",
		"--authorization-mode":                "Webhook",
		"--azure-container-registry-config":   "/etc/kubernetes/azure.json",
		"--cgroups-per-qos":                   "true",
		"--client-ca-file":                    "/etc/kubernetes/certs/ca.crt",
		"--cloud-config":                      "/etc/kubernetes/azure.json",
		"--cloud-provider":                    "external",
		"--cluster-dns":                       "10.0.0.10",
		"--cluster-domain":                    "cluster.local",
		"--enforce-node-allocatable":          "pods",
		"--event-qps":                         "0",
		"--eviction-hard":                     "memory.available<750Mi,nodefs.available<10%,nodefs.inodesFree<5%",
		"--feature-gates":                     "CSIMigration=true,CSIMigrationAzureDisk=true,CSIMigrationAzureFile=true,DelegateFSGroupToCSIDriver=true,RotateKubeletServerCertificate=true",
		"--image-gc-high-threshold":           "85",
		"--image-gc-low-threshold":            "80",
		"--keep-terminated-pod-volumes":       "false",
		"--kubeconfig":                        "/var/lib/kubelet/kubeconfig",
		"--max-pods":                          "110",
		"--node-status-update-frequency":      "10s",
		"--pod-infra-container-image":         "mcr.microsoft.com/oss/kubernetes/pause:3.6",
		"--pod-manifest-path":                 "/etc/kubernetes/manifests",
		"--pod-max-pids":                      "-1",
		"--protect-kernel-defaults":           "true",
		"--read-only-port":                    "0",
		"--resolv-conf":                       "/run/systemd/resolve/resolv.conf",
		"--rotate-certificates":               "true",
		"--streaming-connection-idle-timeout": "4h",
		"--tls-cert-file":                     "/etc/kubernetes/certs/kubeletserver.crt",
		"--tls-cipher-suites":                 "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_256_GCM_SHA384,TLS_RSA_WITH_AES_128_GCM_SHA256",
		"--tls-private-key-file":              "/etc/kubernetes/certs/kubeletserver.key",
	}

	kubeletNodeLabelsBase = map[string]string{
		"kubernetes.azure.com/mode": "system", // TODO: required? (came from agentPoolProfile.customNodeLabels)
		// "kubernetes.azure.com/node-image-version": "AKSUbuntu-1804gen2containerd-2022.01.19", // TODO: set dynamically (if required) (came from agentPoolProfile.customNodeLabels)
	}
)

var (

	// baseline, covering unused (-), static (s), and unsupported (n) fields,
	// as well as defaults, cluster/node level (cd/td/xd)
	staticNodeBootstrapVars = NodeBootstrapVariables{
		IsAKSCustomCloud:                false,        // n
		InitAKSCustomCloudFilepath:      "",           // n
		AKSCustomCloudRepoDepotEndpoint: "",           // n
		AdminUsername:                   "azureuser",  // td
		MobyVersion:                     "",           // -
		HyperkubeURL:                    "",           // -
		KubeBinaryURL:                   "",           // cd
		CustomKubeBinaryURL:             "",           // -
		KubeproxyURL:                    "",           // -
		VMType:                          "vmss",       // xd
		Subnet:                          "aks-subnet", // xd
		VirtualNetworkResourceGroup:     "",           // xd
		PrimaryAvailabilitySet:          "",           // -
		PrimaryScaleSet:                 "",           // -
		ServicePrincipalClientID:        "msi",        // ad

		// TODO: validate these are still required, and whether they actually need to be set correctly (e.g. azure/kubenet) or just filled in
		VNETCNILinuxPluginsURL: "https://acs-mirror.azureedge.net/azure-cni/v1.4.32/binaries/azure-vnet-cni-linux-amd64-v1.4.32.tgz", // - [currently required, installCNI in provisioning scripts depends on CNI_PLUGINS_URL]
		CNIPluginsURL:          "https://acs-mirror.azureedge.net/cni-plugins/v1.1.1/binaries/cni-plugins-linux-amd64-v1.1.1.tgz",    // - [currently required, same]

		CloudProviderBackoff:              true,         // s
		CloudProviderBackoffMode:          "v2",         // s
		CloudProviderBackoffRetries:       "6",          // s
		CloudProviderBackoffExponent:      "0",          // s
		CloudProviderBackoffDuration:      "5",          // s
		CloudProviderBackoffJitter:        "0",          // s
		CloudProviderRatelimit:            true,         // s
		CloudProviderRatelimitQPS:         "10",         // s
		CloudProviderRatelimitQPSWrite:    "10",         // s
		CloudProviderRatelimitBucket:      "100",        // s
		CloudProviderRatelimitBucketWrite: "100",        // s
		LoadBalancerDisableOutboundSNAT:   false,        // xd
		UseManagedIdentityExtension:       false,        // s
		UseInstanceMetadata:               true,         // s
		LoadBalancerSKU:                   "Standard",   // xd
		ExcludeMasterFromStandardLB:       true,         // s
		MaximumLoadbalancerRuleCount:      250,          // xd
		ContainerRuntime:                  "containerd", // s
		CLITool:                           "ctr",        // s
		ContainerdDownloadURLBase:         "",           // -
		NetworkMode:                       "",           // cd
		IsVHD:                             true,         // s
		SGXNode:                           false,        // -
		MIGNode:                           false,        // td
		ConfigGPUDriverIfNeeded:           true,         // s
		EnableGPUDevicePluginIfNeeded:     false,        // -
		TeleportdPluginDownloadURL:        "",           // -
		ContainerdVersion:                 "",           // -
		ContainerdPackageURL:              "",           // -
		RuncVersion:                       "",           // -
		RuncPackageURL:                    "",           // -
		DisableSSH:                        false,        // td
		EnableHostsConfigAgent:            false,        // n
		NeedsContainerd:                   true,         // s
		TeleportEnabled:                   false,        // td
		ShouldConfigureHTTPProxy:          false,        // cd
		ShouldConfigureHTTPProxyCA:        false,        // cd
		HTTPProxyTrustedCA:                "",           // cd
		ShouldConfigureCustomCATrust:      false,        // cd
		CustomCATrustConfigCerts:          []string{},   // cd

		OutboundCommand:                 "curl -v --insecure --proxy-insecure https://mcr.microsoft.com/v2/", // s
		EnableUnattendedUpgrades:        true,                                                                // cd
		IsKrustlet:                      false,                                                               // td
		ShouldConfigSwapFile:            false,                                                               // td
		ShouldConfigTransparentHugePage: false,                                                               // td
		TargetCloud:                     "AzurePublicCloud",                                                  // n
		TargetEnvironment:               "AzurePublicCloud",                                                  // n
		CustomEnvJSON:                   "",                                                                  // n
		IsCustomCloud:                   false,                                                               // n
		CSEHelpersFilepath:              "/opt/azure/containers/provision_source.sh",                         // s
		CSEDistroHelpersFilepath:        "/opt/azure/containers/provision_source_distro.sh",                  // s
		CSEInstallFilepath:              "/opt/azure/containers/provision_installs.sh",                       // s
		CSEDistroInstallFilepath:        "/opt/azure/containers/provision_installs_distro.sh",                // s
		CSEConfigFilepath:               "/opt/azure/containers/provision_configs.sh",                        // s
		AzurePrivateRegistryServer:      "",                                                                  // cd
		HasCustomSearchDomain:           false,                                                               // cd
		CustomSearchDomainFilepath:      "/opt/azure/containers/setup-custom-search-domains.sh",              // s
		HTTPProxyURLs:                   "",                                                                  // cd
		HTTPSProxyURLs:                  "",                                                                  // cd
		NoProxyURLs:                     "",                                                                  // cd
		ClientTLSBootstrappingEnabled:   true,                                                                // s
		THPEnabled:                      "",                                                                  // cd
		THPDefrag:                       "",                                                                  // cd
		ServicePrincipalFileContent:     base64.StdEncoding.EncodeToString([]byte("msi")),                    // s
		KubeletClientContent:            "",                                                                  // -
		KubeletClientCertContent:        "",                                                                  // -
		KubeletConfigFileEnabled:        false,                                                               // s
		KubeletConfigFileContent:        "",                                                                  // s
		SwapFileSizeMB:                  0,                                                                   // td
		GPUInstanceProfile:              "",                                                                  // td
		CustomSearchDomainName:          "",                                                                  // cd
		CustomSearchRealmUser:           "",                                                                  // cd
		CustomSearchRealmPassword:       "",                                                                  // cd
		MessageOfTheDay:                 "",                                                                  // td
		HasKubeletDiskType:              false,                                                               // td
		SysctlContent:                   base64.StdEncoding.EncodeToString(sysctlContent),                    // td
		KubeletFlags:                    "",                                                                  // psX
		AzureEnvironmentFilepath:        "",                                                                  // s
		KubenetTemplate:                 base64.StdEncoding.EncodeToString(kubenetTemplate),                  // s
		ContainerdConfigContent:         base64.StdEncoding.EncodeToString(containerdConfigContent),          // kd
		IsKata:                          false,                                                               // n
	}
)

func (a AKS) aksBootstrapScript() (string, error) {
	// use these as the base / defaults
	nbv := staticNodeBootstrapVars // don't need deep copy (yet)

	// apply overrides from passed in options
	a.applyOptions(&nbv)

	// generate script from template using the variables
	customData, err := getCustomDataFromNodeBootstrapVars(&nbv)
	if err != nil {
		return "", fmt.Errorf("error getting custom data from node bootstrap variables: %w", err)
	}
	return customData, nil
}

// TODO: ARM64 support
// TODO: Remove when image selection selects vhd based on version
// with proper binaries cached in the vhd
func kubeBinaryURL(kubernetesVersion string) string {
	version, _ := semver.NewVersion(kubernetesVersion)
	var kubeBinaryVersion string
	if version.Minor() == 24 {
		kubeBinaryVersion = "1.24.10-hotfix.20230509"
	} else if version.Minor() == 25 {
		kubeBinaryVersion = "1.25.6-hotfix.20230509"
	} else if version.Minor() == 26 {
		kubeBinaryVersion = "1.26.3-hotfix.20230509"
	} else {
		kubeBinaryVersion = "1.27.1"
	}
	return fmt.Sprintf("https://acs-mirror.azureedge.net/kubernetes/v%s/binaries/kubernetes-node-linux-amd64.tar.gz", kubeBinaryVersion)
}

func (a AKS) applyOptions(nbv *NodeBootstrapVariables) {
	nbv.KubeCACrt = *a.CABundle
	nbv.APIServerName = a.APIServerName
	nbv.TLSBootstrapToken = a.KubeletClientTLSBootstrapToken

	nbv.TenantID = a.TenantID
	nbv.SubscriptionID = a.SubscriptionID
	nbv.Location = a.Location
	nbv.ResourceGroup = a.ResourceGroup
	nbv.UserAssignedIdentityID = a.UserAssignedIdentityID

	nbv.NetworkPlugin = a.NetworkPlugin
	nbv.NetworkPolicy = a.NetworkPolicy
	nbv.KubernetesVersion = a.KubernetesVersion
	nbv.KubeBinaryURL = kubeBinaryURL(a.KubernetesVersion)
	// calculated values
	nbv.EnsureNoDupePromiscuousBridge = nbv.NeedsContainerd && nbv.NetworkPlugin == "kubenet" && nbv.NetworkPolicy != "calico"
	nbv.NetworkSecurityGroup = fmt.Sprintf("aks-agentpool-%s-nsg", a.ClusterID)
	nbv.VirtualNetwork = fmt.Sprintf("aks-vnet-%s", a.ClusterID)
	nbv.RouteTable = fmt.Sprintf("aks-agentpool-%s-routetable", a.ClusterID)

	// merge and stringify labels
	kubeletLabels := lo.Assign(kubeletNodeLabelsBase, a.Labels)
	// TODO: revisit name for Karpenter agent pool
	// note: ccp-webhook crashes on encountering nodepool label referencing unknown nodepool (in validateNodeLabelUpdateOrAddRequests when findLabelsForAgentPool is nil)
	const karpenterAgentPoolName = "nodepool1"
	getAgentbakerGeneratedLabels(a.ResourceGroup, karpenterAgentPoolName, kubeletLabels) // custom name for karpenter agent pool for node labeling purposes
	nbv.KubeletNodeLabels = strings.Join(lo.MapToSlice(kubeletLabels, func(k, v string) string {
		return fmt.Sprintf("%s=%s", k, v)
	}), ",")

	// merge and stringify taints
	kubeletFlags := lo.Assign(kubeletFlagsBase)
	if len(a.Taints) > 0 {
		taintStrs := lo.Map(a.Taints, func(taint v1.Taint, _ int) string { return taint.ToString() })
		kubeletFlags = lo.Assign(kubeletFlags, map[string]string{"--register-with-taints": strings.Join(taintStrs, ",")})
	}

	// TODO: merge/override kubelet flags with a.KubeletConfig

	// striginify kubelet flags (including taints)
	nbv.KubeletFlags = strings.Join(lo.MapToSlice(kubeletFlags, func(k, v string) string {
		return fmt.Sprintf("%s=%s", k, v)
	}), " ")
}

func getCustomDataFromNodeBootstrapVars(nbv *NodeBootstrapVariables) (string, error) {
	var buffer bytes.Buffer
	if err := customDataTemplate.Execute(&buffer, *nbv); err != nil {
		return "", fmt.Errorf("error executing custom data template: %w", err)
	}
	return buffer.String(), nil
}

func getAgentbakerGeneratedLabels(nodeResourceGroup string, apName string, nodeLabels map[string]string) {
	nodeLabels["kubernetes.azure.com/role"] = "agent"
	nodeLabels["agentpool"] = apName
	nodeLabels["kubernetes.azure.com/agentpool"] = apName
	nodeLabels["kubernetes.azure.com/cluster"] = normalizeResourceGroupNameForLabel(nodeResourceGroup)

	/*
		if strings.EqualFold(ap.Properties.StorageProfile, "ManagedDisks") {
			storageTier, err := getStorageAccountType(ap.Properties.VMSize)
			nodeLabels["storageprofile"] = "managed"
			nodeLabels["kubernetes.azure.com/storageprofile"] = "managed"
			nodeLabels["storagetier"] = storageTier
			nodeLabels["kubernetes.azure.com/storagetier"] = storageTier
		}
		if crphelper.IsNvidiaEnabledSKU(ap.Properties.VMSize) {
			// label key accelerator will be depreated soon
			nodeLabels["accelerator"] = "nvidia"
			nodeLabels["kubernetes.azure.com/accelerator"] = "nvidia"
		}
		if ap.Properties.IsFIPSEnabled() {
			nodeLabels["kubernetes.azure.com/fips_enabled"] = "true""
		}
		if ap.Properties.OSSKU.StringWithEmpty() != "" {
			nodeLabels["kubernetes.azure.com/os-sku"] = osSku
		}
		if ap.Properties.IsCVM2004() {
			nodeLabels["kubernetes.azure.com/security-type"] = "ConfidentialVM"
		}
		if ap.Properties.IsUsingTrustedLaunchVHD() {
			nodeLabels["kubernetes.azure.com/security-type"] = "TrustedLaunch"
		}
		for _, runtime := range ap.Properties.RuntimeClasses {
			nodeLabels["kubernetes.azure.com/" + runtime.Selector] = "true"
		}
		if ap.Properties.GetEnableCustomCATrust() {
			nodeLabels["kubernetes.azure.com/custom-ca-trust-enabled"] = "true"
		}
		if ap.Properties.IsMarinerKata() {
			nodeLabels["kubernetes.azure.com/kata-mshv-vm-isolation"] = "true"
		}
	*/
}

/*
// getStorageAccountType returns the support managed disk storage tier for a give VM size
func getStorageAccountType(sizeName string) (string, error) {
	spl := strings.Split(sizeName, "_")
	if len(spl) < 2 {
		return "", pkgerrors.Errorf("Invalid sizeName: %s", sizeName)
	}
	capability := spl[1]
	if strings.Contains(strings.ToLower(capability), "s") {
		return "Premium_LRS", nil
	}
	return "Standard_LRS", nil
}
*/

// copy from github.com/Azure/agentbaker/pkg/agent/baker.go, can't directly use since it's not public
func normalizeResourceGroupNameForLabel(resourceGroupName string) string {
	truncated := resourceGroupName
	truncated = strings.ReplaceAll(truncated, "(", "-")
	truncated = strings.ReplaceAll(truncated, ")", "-")
	const maxLen = 63
	if len(truncated) > maxLen {
		truncated = truncated[0:maxLen]
	}

	if strings.HasSuffix(truncated, "-") ||
		strings.HasSuffix(truncated, "_") ||
		strings.HasSuffix(truncated, ".") {
		if len(truncated) > 62 {
			return truncated[0:len(truncated)-1] + "z"
		}
		return truncated + "z"
	}
	return truncated
}