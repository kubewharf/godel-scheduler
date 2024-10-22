/*
Copyright 2024 The Godel Scheduler Authors.

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

package options

import (
	"fmt"
	"net"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	apiserveroptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	clientset "k8s.io/client-go/kubernetes"
	clientgokubescheme "k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/record"
	cliflag "k8s.io/component-base/cli/flag"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/klog/v2"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	cmdconfig "github.com/kubewharf/godel-scheduler/cmd/controller/app/config"
	"github.com/kubewharf/godel-scheduler/pkg/apis/config"
	godelctrlmgrconfig "github.com/kubewharf/godel-scheduler/pkg/controller/apis/config"
	godelctrlmgrconfigscheme "github.com/kubewharf/godel-scheduler/pkg/controller/apis/config/scheme"
	godelctrlmgrconfigv1alpha1 "github.com/kubewharf/godel-scheduler/pkg/controller/apis/config/v1alpha1"
)

const DefaultLeaderElectionConfig = "godel-controller-manager"

type GodelControllerManagerOptions struct {
	Generic               *GenericControllerManagerConfigurationOptions
	ReservationController *ReservationControllerOptions
	Tracer                *TracerOptions

	SecureServing           *apiserveroptions.SecureServingOptionsWithLoopback
	CombinedInsecureServing *CombinedInsecureServingOptions
	Authentication          *apiserveroptions.DelegatingAuthenticationOptions
	Authorization           *apiserveroptions.DelegatingAuthorizationOptions

	// ConfigFile is the location of the scheduler server's configuration file.
	ConfigFile string

	// WriteConfigTo is the path where the default configuration will be written.
	WriteConfigTo string

	Master string
}

func NewGodelControllerManagerOptions() (*GodelControllerManagerOptions, error) {
	componentConfig, err := NewDefaultComponentConfig()
	if err != nil {
		return nil, err
	}

	hhost, hport, err := splitHostIntPort(componentConfig.HealthzBindAddress)
	if err != nil {
		return nil, err
	}

	opts := GodelControllerManagerOptions{
		Generic: NewGenericControllerManagerConfigurationOptions(componentConfig.Generic),
		CombinedInsecureServing: &CombinedInsecureServingOptions{
			Healthz: (&apiserveroptions.DeprecatedInsecureServingOptions{
				BindNetwork: "tcp",
			}).WithLoopback(),
			Metrics: (&apiserveroptions.DeprecatedInsecureServingOptions{
				BindNetwork: "tcp",
			}).WithLoopback(),
			BindPort:    hport,
			BindAddress: hhost,
		},

		ReservationController: &ReservationControllerOptions{
			componentConfig.ReservationController,
		},
		Tracer: &TracerOptions{
			componentConfig.Tracer,
		},

		SecureServing:  apiserveroptions.NewSecureServingOptions().WithLoopback(),
		Authentication: apiserveroptions.NewDelegatingAuthenticationOptions(),
		Authorization:  apiserveroptions.NewDelegatingAuthorizationOptions(),
	}

	opts.Authentication.RemoteKubeConfigFileOptional = true
	opts.Authorization.RemoteKubeConfigFileOptional = true

	// Set the PairName but leave certificate directory blank to generate in-memory by default
	opts.SecureServing.ServerCert.CertDirectory = ""
	opts.SecureServing.ServerCert.PairName = DefaultLeaderElectionConfig
	opts.SecureServing.BindPort = godelctrlmgrconfigv1alpha1.GodelControllerManagerSecurePort

	opts.Generic.LeaderElection.ResourceName = DefaultLeaderElectionConfig
	opts.Generic.LeaderElection.ResourceNamespace = config.NamespaceSystem
	return &opts, nil
}

func NewDefaultComponentConfig() (godelctrlmgrconfig.GodelControllerManagerConfiguration, error) {
	versioned := godelctrlmgrconfigv1alpha1.GodelControllerManagerConfiguration{}
	godelctrlmgrconfigscheme.Scheme.Default(&versioned)

	internal := godelctrlmgrconfig.GodelControllerManagerConfiguration{}
	if err := godelctrlmgrconfigscheme.Scheme.Convert(&versioned, &internal, nil); err != nil {
		return internal, err
	}
	return internal, nil
}

func (opt *GodelControllerManagerOptions) Flags(allControllers []string, disabledByDefaultControllers []string) cliflag.NamedFlagSets {
	fss := cliflag.NamedFlagSets{}

	opt.Generic.AddFlags(&fss, allControllers, disabledByDefaultControllers)
	opt.Authentication.AddFlags(fss.FlagSet("authentication"))
	opt.Authorization.AddFlags(fss.FlagSet("authorization"))
	opt.SecureServing.AddFlags(fss.FlagSet("secure serving"))
	opt.CombinedInsecureServing.AddFlags(fss.FlagSet("insecure serving"))

	opt.Tracer.AddFlags(fss.FlagSet("tracer"))
	opt.ReservationController.AddFlags(fss.FlagSet("reservation Controller"))

	fs := fss.FlagSet("misc")
	fs.StringVar(&opt.Master, "master", opt.Master, "The address of the Kubernetes API server (overrides any value in kubeconfig).")
	fs.StringVar(&opt.Generic.ClientConnection.Kubeconfig, "kubeconfig", opt.Generic.ClientConnection.Kubeconfig, "Path to kubeconfig file with authorization and master location information (the master location can be overridden by the master flag).")
	utilfeature.DefaultMutableFeatureGate.AddFlag(fss.FlagSet("generic"))
	return fss
}

func (opt *GodelControllerManagerOptions) ApplyTo(c *cmdconfig.Config) error {
	if err := opt.Generic.ApplyTo(c.ComponentConfig.Generic); err != nil {
		return err
	}

	if err := opt.ReservationController.ApplyTo(c.ComponentConfig.ReservationController); err != nil {
		return err
	}

	opt.Tracer.ApplyTo(c.ComponentConfig.Tracer)

	if err := opt.SecureServing.ApplyTo(&c.SecureServing, &c.LoopbackClientConfig); err != nil {
		return err
	}

	if opt.SecureServing.BindPort != 0 || opt.SecureServing.Listener != nil {
		if err := opt.Authentication.ApplyTo(&c.Authentication, c.SecureServing, nil); err != nil {
			return err
		}
		if err := opt.Authorization.ApplyTo(&c.Authorization); err != nil {
			return err
		}
	}

	if err := opt.CombinedInsecureServing.ApplyTo(c, c.ComponentConfig); err != nil {
		return err
	}

	return nil
}

func (opt *GodelControllerManagerOptions) Validate(allControllers []string, disabledByDefaultControllers []string) error {
	var errs []error

	errs = append(errs, opt.Generic.Validate(allControllers, disabledByDefaultControllers)...)
	errs = append(errs, opt.SecureServing.Validate()...)
	errs = append(errs, opt.Authentication.Validate()...)
	errs = append(errs, opt.Authorization.Validate()...)
	errs = append(errs, opt.Tracer.Validate())

	return utilerrors.NewAggregate(errs)
}

func (opt *GodelControllerManagerOptions) Config(allControllers []string, disabledByDefaultControllers []string) (*cmdconfig.Config, error) {
	if err := opt.Validate(allControllers, disabledByDefaultControllers); err != nil {
		return nil, err
	}

	if err := opt.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
	}

	c := &cmdconfig.Config{
		ComponentConfig: godelctrlmgrconfig.NewEmptyGodelControllerManagerConfiguration(),
	}

	if err := opt.ApplyTo(c); err != nil {
		return nil, err
	}

	// Prepare kube clients.
	kubeconfig, client, crdKubeconfig, godelCrdClient, err := createClients(c.ComponentConfig.Generic.ClientConnection, opt.Master, c.ComponentConfig.Generic.LeaderElection.RenewDeadline.Duration)
	if err != nil {
		return nil, err
	}

	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(clientgokubescheme.Scheme, v1.EventSource{Component: DefaultLeaderElectionConfig})

	c.Client = client
	c.GodelClient = godelCrdClient
	c.Kubeconfig = kubeconfig
	c.CrdKubeconfig = crdKubeconfig
	c.EventBroadcaster = eventBroadcaster
	c.EventRecorder = eventRecorder

	return c, nil
}

func splitHostIntPort(s string) (string, int, error) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, err
	}
	portInt, err := strconv.Atoi(port)
	if err != nil {
		return "", 0, err
	}
	return host, portInt, err
}

// createClients creates a kube client and an event client from the given config and masterOverride.
// TODO remove masterOverride when CLI flags are removed.
func createClients(config componentbaseconfig.ClientConnectionConfiguration, masterOverride string, timeout time.Duration) (*restclient.Config, *clientset.Clientset, *restclient.Config, *godelclient.Clientset, error) {
	if len(config.Kubeconfig) == 0 && len(masterOverride) == 0 {
		klog.InfoS("Neither --kubeconfig nor --master was specified. Using default API client. This might not work.")
	}

	kubeconfig, err := clientcmd.BuildConfigFromFlags(masterOverride, config.Kubeconfig)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	kubeconfig.DisableCompression = true
	kubeconfig.ContentConfig.AcceptContentTypes = config.AcceptContentTypes
	kubeconfig.ContentConfig.ContentType = config.ContentType
	kubeconfig.QPS = config.QPS
	kubeconfig.Burst = int(config.Burst)

	client, err := clientset.NewForConfig(restclient.AddUserAgent(kubeconfig, "godel-controller-manager"))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// This creates a client, first loading any specified kubeconfig
	// file, and then overriding the Master flag, if non-empty.
	crdKubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterOverride}}).ClientConfig()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	crdKubeConfig.DisableCompression = true
	crdKubeConfig.QPS = config.QPS
	crdKubeConfig.Burst = int(config.Burst)

	godelCrdClient, err := godelclient.NewForConfig(restclient.AddUserAgent(crdKubeConfig, "godel-controller-manager"))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return kubeconfig, client, crdKubeConfig, godelCrdClient, nil
}
