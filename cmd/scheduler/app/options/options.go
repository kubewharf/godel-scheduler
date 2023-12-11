/*
Copyright 2023 The Godel Scheduler Authors.

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
	"os"
	"strconv"
	"time"

	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	godelclientscheme "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned/scheme"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	katalystclient "github.com/kubewharf/katalyst-api/pkg/client/clientset/versioned"
	katalystinformers "github.com/kubewharf/katalyst-api/pkg/client/informers/externalversions"
	"github.com/spf13/pflag"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/validation/field"
	apiserveroptions "k8s.io/apiserver/pkg/server/options"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	clientset "k8s.io/client-go/kubernetes"
	clientsetscheme "k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/tools/record"
	cliflag "k8s.io/component-base/cli/flag"
	componentbaseconfig "k8s.io/component-base/config/v1alpha1"
	"k8s.io/klog/v2"

	schedulerappconfig "github.com/kubewharf/godel-scheduler/cmd/scheduler/app/config"
	"github.com/kubewharf/godel-scheduler/cmd/scheduler/app/util/ports"
	defaultsconfig "github.com/kubewharf/godel-scheduler/pkg/apis/config"
	godelschedulerconfig "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelschedulerscheme "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/scheme"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/validation"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
)

var DefaultLeaderElectionConfig = "scheduler"

type Options struct {
	// The default values. These are overridden if ConfigFile is set or by values in InsecureServing.
	ComponentConfig godelschedulerconfig.GodelSchedulerConfiguration

	SecureServing           *apiserveroptions.SecureServingOptionsWithLoopback
	CombinedInsecureServing *CombinedInsecureServingOptions
	Authentication          *apiserveroptions.DelegatingAuthenticationOptions
	Authorization           *apiserveroptions.DelegatingAuthorizationOptions

	// ConfigFile is the location of the scheduler server's configuration file.
	ConfigFile string

	// WriteConfigTo is the path where the default configuration will be written.
	WriteConfigTo string

	Master string

	// scheduler renew period in seconds
	SchedulerRenewIntervalSeconds int64

	// TODO: The following fields are reserved for backward compatibility only.
	// We need to remove this logic in the near future.
	UnitMaxBackoffSeconds         int64
	UnitInitialBackoffSeconds     int64
	DisablePreemption             bool
	AttemptImpactFactorOnPriority float64
}

// NewOptions returns default scheduler app options.
func NewOptions() (*Options, error) {
	cfg, err := newDefaultComponentConfig()
	if err != nil {
		return nil, err
	}

	hhost, hport, err := splitHostIntPort(cfg.HealthzBindAddress)
	if err != nil {
		return nil, err
	}

	o := &Options{
		ComponentConfig: *cfg,
		SecureServing:   apiserveroptions.NewSecureServingOptions().WithLoopback(),
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
		Authentication:                apiserveroptions.NewDelegatingAuthenticationOptions(),
		Authorization:                 apiserveroptions.NewDelegatingAuthorizationOptions(),
		SchedulerRenewIntervalSeconds: godelschedulerconfig.DefaultRenewIntervalInSeconds,
	}

	o.Authentication.TolerateInClusterLookupFailure = true
	o.Authentication.RemoteKubeConfigFileOptional = true
	o.Authorization.RemoteKubeConfigFileOptional = true
	o.Authorization.AlwaysAllowPaths = []string{"/healthz"}

	// Set the PairName but leave certificate directory blank to generate in-memory by default
	o.SecureServing.ServerCert.CertDirectory = ""
	o.SecureServing.ServerCert.PairName = "scheduler"

	o.SecureServing.BindPort = ports.KubeSchedulerPort

	{
		// TODO: The following fields are reserved for backward compatibility only.
		// We need to remove this logic in the near future.
		o.UnitMaxBackoffSeconds = godelschedulerconfig.DefaultUnitMaxBackoffInSeconds
		o.UnitInitialBackoffSeconds = godelschedulerconfig.DefaultUnitInitialBackoffInSeconds
		o.AttemptImpactFactorOnPriority = godelschedulerconfig.DefaultAttemptImpactFactorOnPriority
		o.DisablePreemption = godelschedulerconfig.DefaultDisablePreemption
	}

	return o, nil
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

func newDefaultComponentConfig() (*godelschedulerconfig.GodelSchedulerConfiguration, error) {
	cfg := godelschedulerconfig.GodelSchedulerConfiguration{}

	godelschedulerscheme.Scheme.Default(&cfg)
	return &cfg, nil
}

// Flags returns flags for a specific scheduler by section name
func (o *Options) Flags() (nfs cliflag.NamedFlagSets) {
	fs := nfs.FlagSet("misc")
	fs.StringVar(&o.ConfigFile, "config", o.ConfigFile, "The path to the configuration file. Flags override values in this file.")
	fs.StringVar(&o.WriteConfigTo, "write-config-to", o.WriteConfigTo, "If set, write the configuration values to this file and exit.")
	fs.StringVar(&o.Master, "master", o.Master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")

	fs.StringVar(&o.ComponentConfig.ClientConnection.Kubeconfig, "kubeconfig", o.ComponentConfig.ClientConnection.Kubeconfig, "path to kubeconfig file with authorization and master location information.")
	fs.Float32Var(&o.ComponentConfig.ClientConnection.QPS, "kube-api-qps", o.ComponentConfig.ClientConnection.QPS, "QPS to use while talking with kubernetes apiserver. This parameter overrides the value defined in config file, which is specified in --config.")
	fs.Int32Var(&o.ComponentConfig.ClientConnection.Burst, "kube-api-burst", o.ComponentConfig.ClientConnection.Burst, "burst to use while talking with kubernetes apiserver. This parameter overrides the value defined in config file, which is specified in --config.")

	o.addSchedulerConfigFlags(nfs.FlagSet("scheduler configuration"))

	o.SecureServing.AddFlags(nfs.FlagSet("secure serving"))
	o.CombinedInsecureServing.AddFlags(nfs.FlagSet("insecure serving"))
	o.Authentication.AddFlags(nfs.FlagSet("authentication"))
	o.Authorization.AddFlags(nfs.FlagSet("authorization"))
	o.ComponentConfig.Tracer.AddFlags(nfs.FlagSet("tracer"))

	BindFlags(&o.ComponentConfig.LeaderElection, nfs.FlagSet("leader election"))
	utilfeature.DefaultMutableFeatureGate.AddFlag(nfs.FlagSet("feature gate"))

	fs.Int64Var(&o.SchedulerRenewIntervalSeconds, "scheduler-renew-interval", o.SchedulerRenewIntervalSeconds, "interval period to use while update scheduler crd on kubernetes apiserver. This parameter overrides the value defined in config file, which is specified in --config.")

	{
		// TODO: The following fields are reserved for backward compatibility only.
		// We need to remove this logic in the near future.
		fs.Int64Var(&o.UnitMaxBackoffSeconds, "max-backoff-seconds", o.UnitMaxBackoffSeconds, "max backoff period for units from backoffQ to enter activeQ, in seconds. This parameter overrides the value defined in config file, which is specified in --config.")
		fs.Int64Var(&o.UnitInitialBackoffSeconds, "initial-backoff-seconds", o.UnitInitialBackoffSeconds, "initial backoff for units from backoffQ to enter activeQ, in seconds. If specified, it must be greater than 0. This parameter overrides the value defined in config file, which is specified in --config.")
		fs.BoolVar(&o.DisablePreemption, "disable-preemption", o.DisablePreemption, "Flag to disable preemption. This parameter overrides the value defined in config file, which is specified in --config.")
		fs.Float64Var(&o.AttemptImpactFactorOnPriority, "attempt-impact-factor-on-priority", o.AttemptImpactFactorOnPriority, "factor used in godel sort to get scheduling attempts impact, the bigger the factor is, the more impact one scheduling attempt will make, default value is 2.0. This parameter overrides the value defined in config file, which is specified in --config.")
	}

	return nfs
}

func (o *Options) addSchedulerConfigFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.ComponentConfig.GodelSchedulerName, "godel-scheduler-name", o.ComponentConfig.GodelSchedulerName, "godel scheduler name, to register scheduler crd.")
	fs.StringVar(o.ComponentConfig.SchedulerName, "scheduler-name", *o.ComponentConfig.SchedulerName, "components will deal with pods that pod.Spec.SchedulerName is equal to scheduler-name / is default-scheduler or empty. This parameter overrides the value defined in config file, which is specified in --config.")
	fs.StringVar(o.ComponentConfig.SubClusterKey, "sub-cluster-key", *o.ComponentConfig.SubClusterKey, "the key to determine a sub cluster. This parameter overrides the value defined in config file, which is specified in --config.")
}

// ApplyTo applies the scheduler options to the given scheduler app configuration.
func (o *Options) ApplyTo(c *schedulerappconfig.Config) error {
	if len(o.ConfigFile) == 0 {
		c.ComponentConfig = o.ComponentConfig

		if err := o.CombinedInsecureServing.ApplyTo(c, &c.ComponentConfig); err != nil {
			return err
		}
	} else {
		cfg, err := loadConfigFromFile(o.ConfigFile)
		if err != nil {
			return err
		}

		if err := validation.ValidateGodelSchedulerConfiguration(cfg).ToAggregate(); err != nil {
			return err
		}

		toUse := cfg.DeepCopy()

		// 1. LeaderElection & SchedulerRenewIntervalSeconds
		{
			// if leader election configuration is set, replace config from options
			if !*o.ComponentConfig.LeaderElection.LeaderElect {
				toUse.LeaderElection.LeaderElect = o.ComponentConfig.LeaderElection.LeaderElect
			}
			if o.ComponentConfig.LeaderElection.ResourceLock != defaultsconfig.DefaultLeaseLock {
				toUse.LeaderElection.ResourceLock = o.ComponentConfig.LeaderElection.ResourceLock
			}
			if o.ComponentConfig.LeaderElection.ResourceNamespace != defaultsconfig.NamespaceSystem {
				toUse.LeaderElection.ResourceNamespace = o.ComponentConfig.LeaderElection.ResourceNamespace
			}
			if o.ComponentConfig.LeaderElection.ResourceName != godelschedulerconfig.DefaultSchedulerName {
				toUse.LeaderElection.ResourceName = o.ComponentConfig.LeaderElection.ResourceName
			}
			if o.ComponentConfig.LeaderElection.LeaseDuration != defaultsconfig.DefaultLeaseDuration {
				toUse.LeaderElection.LeaseDuration = o.ComponentConfig.LeaderElection.LeaseDuration
			}
			if o.ComponentConfig.LeaderElection.RenewDeadline != defaultsconfig.DefaultLeaseRenewDeadline {
				toUse.LeaderElection.RenewDeadline = o.ComponentConfig.LeaderElection.RenewDeadline
			}
			if o.ComponentConfig.LeaderElection.RetryPeriod != defaultsconfig.DefaultLeaseRetryPeriod {
				toUse.LeaderElection.RetryPeriod = o.ComponentConfig.LeaderElection.RetryPeriod
			}
			// if schedulerRenewInterval is not set as default
			if o.SchedulerRenewIntervalSeconds != godelschedulerconfig.DefaultRenewIntervalInSeconds {
				toUse.SchedulerRenewIntervalSeconds = o.SchedulerRenewIntervalSeconds
			}
		}
		// 2. ClientConnection and BindSetting
		{
			// if client connection configuration is set, replace config from options
			if o.ComponentConfig.ClientConnection.QPS != godelschedulerconfig.DefaultClientConnectionQPS {
				toUse.ClientConnection.QPS = o.ComponentConfig.ClientConnection.QPS
			}
			if o.ComponentConfig.ClientConnection.Burst != godelschedulerconfig.DefaultClientConnectionBurst {
				toUse.ClientConnection.Burst = o.ComponentConfig.ClientConnection.Burst
			}
			if len(o.ComponentConfig.ClientConnection.Kubeconfig) != 0 {
				toUse.ClientConnection.Kubeconfig = o.ComponentConfig.ClientConnection.Kubeconfig
			}

		}
		// 3. DebuggingConfiguration
		// do nothing

		// 4. Godel Scheduler
		{
			// use the loaded config file if options are not set to default
			// if scheduler name specified, replace the default value.
			if o.ComponentConfig.GodelSchedulerName != godelschedulerconfig.DefaultGodelSchedulerName {
				toUse.GodelSchedulerName = o.ComponentConfig.GodelSchedulerName
			}
			if *o.ComponentConfig.SchedulerName != godelschedulerconfig.DefaultSchedulerName {
				toUse.SchedulerName = o.ComponentConfig.SchedulerName
			}
			if *o.ComponentConfig.Tracer.Tracer != godelschedulerconfig.DefaultTracer {
				toUse.Tracer.Tracer = o.ComponentConfig.Tracer.Tracer
			}
			if *o.ComponentConfig.Tracer.ClusterName != godelschedulerconfig.DefaultCluster {
				toUse.Tracer.ClusterName = o.ComponentConfig.Tracer.ClusterName
			}
			if *o.ComponentConfig.Tracer.IDCName != godelschedulerconfig.DefaultIDC {
				toUse.Tracer.IDCName = o.ComponentConfig.Tracer.IDCName
			}
			if *o.ComponentConfig.SubClusterKey != godelschedulerconfig.DefaultSubClusterKey {
				toUse.SubClusterKey = o.ComponentConfig.SubClusterKey
			}
		}
		// 5. Godel Profiles (Default)
		{
			// if unitMaxBackoffSeconds is not set as default
			if o.ComponentConfig.DefaultProfile.UnitMaxBackoffSeconds != nil && *o.ComponentConfig.DefaultProfile.UnitMaxBackoffSeconds != godelschedulerconfig.DefaultUnitMaxBackoffInSeconds {
				toUse.DefaultProfile.UnitMaxBackoffSeconds = o.ComponentConfig.DefaultProfile.UnitMaxBackoffSeconds
			}
			// if UnitInitialBackoffSeconds is not set as default
			if o.ComponentConfig.DefaultProfile.UnitInitialBackoffSeconds != nil && *o.ComponentConfig.DefaultProfile.UnitInitialBackoffSeconds != godelschedulerconfig.DefaultUnitInitialBackoffInSeconds {
				toUse.DefaultProfile.UnitInitialBackoffSeconds = o.ComponentConfig.DefaultProfile.UnitInitialBackoffSeconds
			}

			// if attemptImpactFactorOnPriority is not set as default
			if o.ComponentConfig.DefaultProfile.AttemptImpactFactorOnPriority != nil && *o.ComponentConfig.DefaultProfile.AttemptImpactFactorOnPriority != godelschedulerconfig.DefaultAttemptImpactFactorOnPriority {
				toUse.DefaultProfile.AttemptImpactFactorOnPriority = o.ComponentConfig.DefaultProfile.AttemptImpactFactorOnPriority
			}

			// check disable preemption is set
			if o.ComponentConfig.DefaultProfile.DisablePreemption != nil && *o.ComponentConfig.DefaultProfile.DisablePreemption != godelschedulerconfig.DefaultDisablePreemption {
				toUse.DefaultProfile.DisablePreemption = o.ComponentConfig.DefaultProfile.DisablePreemption
			}

			// check block queue is set
			if o.ComponentConfig.DefaultProfile.BlockQueue != nil && *o.ComponentConfig.DefaultProfile.BlockQueue != godelschedulerconfig.DefaultBlockQueue {
				toUse.DefaultProfile.BlockQueue = o.ComponentConfig.DefaultProfile.BlockQueue
			}
		}
		// 6. preemption config
		{
			applyPreemptionConfig(toUse.DefaultProfile, o.ComponentConfig.DefaultProfile)
			for i, subClusterProfile := range toUse.SubClusterProfiles {
				for j, optionSubClusterProfile := range o.ComponentConfig.SubClusterProfiles {
					if subClusterProfile.SubClusterName != optionSubClusterProfile.SubClusterName {
						continue
					}
					applyPreemptionConfig(&toUse.SubClusterProfiles[i], &o.ComponentConfig.SubClusterProfiles[j])
					break
				}
			}
		}

		c.ComponentConfig = *toUse

		// check listen port and override is not default
		if o.CombinedInsecureServing.BindPort != godelschedulerconfig.DefaultInsecureSchedulerPort ||
			o.CombinedInsecureServing.BindAddress != godelschedulerconfig.DefaultGodelSchedulerAddress {
			if err := o.CombinedInsecureServing.ApplyTo(c, &c.ComponentConfig); err != nil {
				return err
			}
		} else if err := o.CombinedInsecureServing.ApplyToFromLoadedConfig(c, &c.ComponentConfig); err != nil {
			return err
		}
	}

	if err := o.SecureServing.ApplyTo(&c.SecureServing, &c.LoopbackClientConfig); err != nil {
		return err
	}
	if o.SecureServing != nil && (o.SecureServing.BindPort != 0 || o.SecureServing.Listener != nil) {
		if err := o.Authentication.ApplyTo(&c.Authentication, c.SecureServing, nil); err != nil {
			return err
		}
		if err := o.Authorization.ApplyTo(&c.Authorization); err != nil {
			return err
		}
	}

	// TODO: The following fields are reserved for backward compatibility only.
	// We need to remove this logic in the near future.
	//
	// Overwrite Godel Profiles (Default)
	{
		if c.ComponentConfig.DefaultProfile == nil {
			c.ComponentConfig.DefaultProfile = &godelschedulerconfig.GodelSchedulerProfile{}
		}
		if o.UnitMaxBackoffSeconds != godelschedulerconfig.DefaultUnitMaxBackoffInSeconds {
			c.ComponentConfig.DefaultProfile.UnitMaxBackoffSeconds = &o.UnitMaxBackoffSeconds
		}
		if o.UnitInitialBackoffSeconds != godelschedulerconfig.DefaultUnitInitialBackoffInSeconds {
			c.ComponentConfig.DefaultProfile.UnitInitialBackoffSeconds = &o.UnitInitialBackoffSeconds
		}
		if o.AttemptImpactFactorOnPriority != godelschedulerconfig.DefaultAttemptImpactFactorOnPriority {
			c.ComponentConfig.DefaultProfile.AttemptImpactFactorOnPriority = &o.AttemptImpactFactorOnPriority
		}
		if o.DisablePreemption != godelschedulerconfig.DefaultDisablePreemption {
			c.ComponentConfig.DefaultProfile.DisablePreemption = &o.DisablePreemption
		}
	}

	return nil
}

func applyPreemptionConfig(configProfile, optionProfile *godelschedulerconfig.GodelSchedulerProfile) {
	if configProfile.CandidatesSelectPolicy == nil {
		if optionProfile != nil && optionProfile.CandidatesSelectPolicy != nil {
			configProfile.CandidatesSelectPolicy = optionProfile.CandidatesSelectPolicy
		}
	}
	if configProfile.BetterSelectPolicies == nil {
		if optionProfile != nil && optionProfile.BetterSelectPolicies != nil {
			configProfile.BetterSelectPolicies = optionProfile.BetterSelectPolicies
		}
	}
}

// Validate validates all the required options.
func (o *Options) Validate() []error {
	var errs []error
	if err := validation.ValidateGodelSchedulerConfiguration(&o.ComponentConfig).ToAggregate(); err != nil {
		errs = append(errs, err.Errors()...)
	}
	errs = append(errs, o.SecureServing.Validate()...)
	errs = append(errs, o.CombinedInsecureServing.Validate()...)
	errs = append(errs, o.Authentication.Validate()...)
	errs = append(errs, o.Authorization.Validate()...)

	if o.SchedulerRenewIntervalSeconds < 0 {
		errs = append(errs, field.Required(field.NewPath("schedulerRenewIntervalSeconds"), "must be greater than 0"))
	}
	return errs
}

// Config return a scheduler config object
func (o *Options) Config() (*schedulerappconfig.Config, error) {
	if o.SecureServing != nil {
		if err := o.SecureServing.MaybeDefaultWithSelfSignedCerts("localhost", nil, []net.IP{net.ParseIP("127.0.0.1")}); err != nil {
			return nil, fmt.Errorf("error creating self-signed certificates: %v", err)
		}
	}

	c := &schedulerappconfig.Config{}
	if err := o.ApplyTo(c); err != nil {
		return nil, err
	}

	// Prepare kube clients.
	// client, leaderElectionClient, eventClient, err := createClients(c.ComponentConfig.ClientConnection, o.Master, c.ComponentConfig.LeaderElection.RenewDeadline.Duration)
	client, leaderElectionClient, eventClient, godelCrdClient, katalystCrdClient, err := createClients(c.ComponentConfig.ClientConnection, o.Master, c.ComponentConfig.LeaderElection.RenewDeadline.Duration)
	if err != nil {
		return nil, err
	}

	c.EventBroadcaster = cmdutil.NewEventBroadcasterAdapter(eventClient)

	// Set up leader election if enabled.
	var leaderElectionConfig *leaderelection.LeaderElectionConfig
	if *c.ComponentConfig.LeaderElection.LeaderElect {
		// Use the scheduler name in the first profile to record leader election.
		coreRecorder := c.EventBroadcaster.DeprecatedNewLegacyRecorder(c.ComponentConfig.GodelSchedulerName)
		leaderElectionConfig, err = makeLeaderElectionConfig(c.ComponentConfig.LeaderElection, leaderElectionClient, coreRecorder)
		if err != nil {
			return nil, err
		}
	}

	c.Client = client
	c.InformerFactory = cmdutil.NewInformerFactory(client, 0)
	c.GodelCrdClient = godelCrdClient
	c.GodelCrdInformerFactory = crdinformers.NewSharedInformerFactory(c.GodelCrdClient, 0)

	c.KatalystCrdClient = katalystCrdClient
	c.KatalystCrdInformerFactory = katalystinformers.NewSharedInformerFactory(c.KatalystCrdClient, 0)

	c.LeaderElection = leaderElectionConfig

	return c, nil
}

// makeLeaderElectionConfig builds a leader election configuration. It will
// create a new resource lock associated with the configuration.
func makeLeaderElectionConfig(config componentbaseconfig.LeaderElectionConfiguration, client clientset.Interface, recorder record.EventRecorder) (*leaderelection.LeaderElectionConfig, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("unable to get hostname: %v", err)
	}
	// add a uniquifier so that two processes on the same host don't accidentally both become active
	id := hostname + "_" + string(uuid.NewUUID())

	rl, err := resourcelock.New(config.ResourceLock,
		config.ResourceNamespace,
		config.ResourceName,
		client.CoreV1(),
		client.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity:      id,
			EventRecorder: recorder,
		})
	if err != nil {
		return nil, fmt.Errorf("couldn't create resource lock: %v", err)
	}

	return &leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: config.LeaseDuration.Duration,
		RenewDeadline: config.RenewDeadline.Duration,
		RetryPeriod:   config.RetryPeriod.Duration,
		WatchDog:      leaderelection.NewLeaderHealthzAdaptor(time.Second * 20),
		Name:          fmt.Sprintf("%s/%s", config.ResourceNamespace, config.ResourceName),
	}, nil
}

// createClients creates a kube client and an event client from the given config and masterOverride.
// TODO remove masterOverride when CLI flags are removed.
func createClients(config componentbaseconfig.ClientConnectionConfiguration, masterOverride string, timeout time.Duration) (clientset.Interface, clientset.Interface, clientset.Interface, godelclient.Interface, katalystclient.Interface, error) {
	if len(config.Kubeconfig) == 0 && len(masterOverride) == 0 {
		klog.InfoS("WARN: Neither --kubeconfig nor --master was specified. Using default API client. This might not work")
	}

	// This creates a client, first loading any specified kubeconfig
	// file, and then overriding the Master flag, if non-empty.
	kubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterOverride}}).ClientConfig()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	kubeConfig.DisableCompression = true
	kubeConfig.AcceptContentTypes = config.AcceptContentTypes
	kubeConfig.ContentType = config.ContentType
	kubeConfig.QPS = config.QPS
	kubeConfig.Burst = int(config.Burst)

	client, err := clientset.NewForConfig(restclient.AddUserAgent(kubeConfig, "scheduler"))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// shallow copy, do not modify the kubeConfig.Timeout.
	restConfig := *kubeConfig
	restConfig.Timeout = timeout
	leaderElectionClient, err := clientset.NewForConfig(restclient.AddUserAgent(&restConfig, "leader-election"))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	utilruntime.Must(godelclientscheme.AddToScheme(clientsetscheme.Scheme))
	eventClient, err := clientset.NewForConfig(kubeConfig)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	// This creates a client, first loading any specified kubeconfig
	// file, and then overriding the Master flag, if non-empty.
	crdKubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterOverride}}).ClientConfig()
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	crdKubeConfig.DisableCompression = true
	crdKubeConfig.QPS = config.QPS
	crdKubeConfig.Burst = int(config.Burst)

	godelCrdClient, err := godelclient.NewForConfig(restclient.AddUserAgent(crdKubeConfig, "scheduler"))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	katalystCrdClient, err := katalystclient.NewForConfig(restclient.AddUserAgent(crdKubeConfig, "scheduler"))
	return client, leaderElectionClient, eventClient, godelCrdClient, katalystCrdClient, nil
}
