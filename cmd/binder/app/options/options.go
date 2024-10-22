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
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
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

	binderappconfig "github.com/kubewharf/godel-scheduler/cmd/binder/app/config"
	defaultconfig "github.com/kubewharf/godel-scheduler/pkg/apis/config"
	binderconfig "github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	"github.com/kubewharf/godel-scheduler/pkg/binder/apis/config/validation"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
)

const DefaultLeaderElectionName = "binder"

type Options struct {
	BinderConfig binderconfig.GodelBinderConfiguration

	// ConfigFile is the location of the scheduler server's configuration file.
	ConfigFile string

	// WriteConfigTo is the path where the default configuration will be written.
	WriteConfigTo string

	Master string

	CombinedInsecureServing *CombinedInsecureServingOptions

	VolumeBindingTimeoutSeconds int64
}

// NewOptions returns default binder app options.
func NewOptions() (*Options, error) {
	cfg := newDefaultBinderConfig()

	hhost, hport, err := splitHostIntPort(cfg.HealthzBindAddress)
	if err != nil {
		return nil, err
	}

	o := &Options{
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
		BinderConfig: *cfg,
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

func newDefaultBinderConfig() *binderconfig.GodelBinderConfiguration {
	cfg := binderconfig.GodelBinderConfiguration{}

	binderconfig.SetDefaults_GodelBinderConfiguration(&cfg)
	return &cfg
}

// Flags returns flags for a specific scheduler by section name
func (o *Options) Flags() (nfs cliflag.NamedFlagSets) {
	fs := nfs.FlagSet("misc")
	fs.StringVar(&o.ConfigFile, "config", o.ConfigFile, "The path to the configuration file. Flags override values in this file.")
	fs.StringVar(&o.WriteConfigTo, "write-config-to", o.WriteConfigTo, "If set, write the configuration values to this file and exit.")
	fs.StringVar(&o.Master, "master", o.Master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	fs.StringVar(&o.BinderConfig.ClientConnection.Kubeconfig, "kubeconfig", o.BinderConfig.ClientConnection.Kubeconfig, "path to kubeconfig file with authorization and master location information.")
	fs.Float32Var(&o.BinderConfig.ClientConnection.QPS, "kube-api-qps", o.BinderConfig.ClientConnection.QPS, "QPS to use while talking with kubernetes apiserver. This parameter is ignored if a config file is specified in --config.")
	fs.Int32Var(&o.BinderConfig.ClientConnection.Burst, "kube-api-burst", o.BinderConfig.ClientConnection.Burst, "burst to use while talking with kubernetes apiserver. This parameter is ignored if a config file is specified in --config.")

	fs.StringVar(o.BinderConfig.SchedulerName, "scheduler-name", *o.BinderConfig.SchedulerName, "components will deal with pods that pod.Spec.SchedulerName is equal to scheduler-name / is default-scheduler or empty.")
	fs.Int64Var(&o.BinderConfig.VolumeBindingTimeoutSeconds, "volume-binding-timeout-seconds", o.BinderConfig.VolumeBindingTimeoutSeconds, "timeout for binding pod volumes")
	fs.Int64Var(&o.BinderConfig.ReservationTimeOutSeconds, "reservation-ttl", o.BinderConfig.ReservationTimeOutSeconds, "how long resources will be reserved (for resource reservation).")

	o.CombinedInsecureServing.AddFlags(nfs.FlagSet("insecure serving"))
	o.BinderConfig.Tracer.AddFlags(nfs.FlagSet("tracer"))

	BindFlags(&o.BinderConfig.LeaderElection, nfs.FlagSet("leader election"))
	utilfeature.DefaultMutableFeatureGate.AddFlag(nfs.FlagSet("feature gate"))

	return nfs
}

// ApplyTo applies the binder options to the given binder app configuration.
func (o *Options) ApplyTo(c *binderappconfig.Config) error {
	if len(o.ConfigFile) == 0 {
		c.BinderConfig = o.BinderConfig
		if err := o.CombinedInsecureServing.ApplyTo(c, &c.BinderConfig); err != nil {
			return err
		}
	} else {
		cfg, err := loadConfigFromFile(o.ConfigFile)
		if err != nil {
			return err
		}

		if err := validation.ValidateGodelBinderConfiguration(cfg).ToAggregate(); err != nil {
			return err
		}

		toUse := cfg.DeepCopy()

		// 1. LeaderElection & SchedulerRenewIntervalSeconds
		{
			// if leader election configuration is set, replace config from options
			if !*o.BinderConfig.LeaderElection.LeaderElect {
				toUse.LeaderElection.LeaderElect = o.BinderConfig.LeaderElection.LeaderElect
			}
			if o.BinderConfig.LeaderElection.ResourceLock != defaultconfig.DefaultLeaseLock {
				toUse.LeaderElection.ResourceLock = o.BinderConfig.LeaderElection.ResourceLock
			}
			if o.BinderConfig.LeaderElection.ResourceNamespace != defaultconfig.NamespaceSystem {
				toUse.LeaderElection.ResourceNamespace = o.BinderConfig.LeaderElection.ResourceNamespace
			}
			if o.BinderConfig.LeaderElection.ResourceName != binderconfig.DefaultSchedulerName {
				toUse.LeaderElection.ResourceName = o.BinderConfig.LeaderElection.ResourceName
			}
			if o.BinderConfig.LeaderElection.LeaseDuration != defaultconfig.DefaultLeaseDuration {
				toUse.LeaderElection.LeaseDuration = o.BinderConfig.LeaderElection.LeaseDuration
			}
			if o.BinderConfig.LeaderElection.RenewDeadline != defaultconfig.DefaultLeaseRenewDeadline {
				toUse.LeaderElection.RenewDeadline = o.BinderConfig.LeaderElection.RenewDeadline
			}
			if o.BinderConfig.LeaderElection.RetryPeriod != defaultconfig.DefaultLeaseRetryPeriod {
				toUse.LeaderElection.RetryPeriod = o.BinderConfig.LeaderElection.RetryPeriod
			}
		}
		// 2. ClientConnection and BindSetting
		{
			// if client connection configuration is set, replace config from options
			if o.BinderConfig.ClientConnection.QPS != binderconfig.DefaultClientConnectionQPS {
				toUse.ClientConnection.QPS = o.BinderConfig.ClientConnection.QPS
			}
			if o.BinderConfig.ClientConnection.Burst != binderconfig.DefaultClientConnectionBurst {
				toUse.ClientConnection.Burst = o.BinderConfig.ClientConnection.Burst
			}
			if len(o.BinderConfig.ClientConnection.Kubeconfig) != 0 {
				toUse.ClientConnection.Kubeconfig = o.BinderConfig.ClientConnection.Kubeconfig
			}

		}
		// 3. DebuggingConfiguration
		// do nothing

		// 4. Godel Binder
		{
			// use the loaded config file if options are not set to default
			if *o.BinderConfig.SchedulerName != binderconfig.DefaultSchedulerName {
				toUse.SchedulerName = o.BinderConfig.SchedulerName
			}
			if *o.BinderConfig.Tracer.Tracer != binderconfig.DefaultTracer {
				toUse.Tracer.Tracer = o.BinderConfig.Tracer.Tracer
			}
			if *o.BinderConfig.Tracer.ClusterName != binderconfig.DefaultCluster {
				toUse.Tracer.ClusterName = o.BinderConfig.Tracer.ClusterName
			}
			if *o.BinderConfig.Tracer.IDCName != binderconfig.DefaultIDC {
				toUse.Tracer.IDCName = o.BinderConfig.Tracer.IDCName
			}
			if o.BinderConfig.ReservationTimeOutSeconds != binderconfig.DefaultReservationTimeOutSeconds {
				toUse.ReservationTimeOutSeconds = o.BinderConfig.ReservationTimeOutSeconds
			}
		}
		// 5. Godel Profiles (Default)
		// nothing to overwrite in this version.

		c.BinderConfig = *toUse

		// check listen port and override is not default
		if o.CombinedInsecureServing.BindPort != binderconfig.DefaultInsecureBinderPort ||
			o.CombinedInsecureServing.BindAddress != binderconfig.DefaultGodelBinderAddress {
			if err := o.CombinedInsecureServing.ApplyTo(c, &c.BinderConfig); err != nil {
				return err
			}
		} else if err := o.CombinedInsecureServing.ApplyToFromLoadedConfig(c, &c.BinderConfig); err != nil {
			return err
		}
	}

	return nil
}

// Validate validates all the required options.
func (o *Options) Validate() []error {
	var errs []error
	if err := validation.ValidateGodelBinderConfiguration(&o.BinderConfig).ToAggregate(); err != nil {
		errs = append(errs, err.Errors()...)
	}
	return errs
}

// Config return a scheduler config object
func (o *Options) Config() (*binderappconfig.Config, error) {
	c := &binderappconfig.Config{}
	if err := o.ApplyTo(c); err != nil {
		return nil, err
	}

	// Prepare kube clients.
	client, leaderElectionClient, eventClient, godelCrdClient, katalystCrdClient, err := createClients(c.BinderConfig.ClientConnection, o.Master, c.BinderConfig.LeaderElection.RenewDeadline.Duration)
	if err != nil {
		return nil, err
	}

	c.EventBroadcaster = cmdutil.NewEventBroadcasterAdapter(eventClient)

	// Set up leader election if enabled.
	var leaderElectionConfig *leaderelection.LeaderElectionConfig
	if *c.BinderConfig.LeaderElection.LeaderElect {
		// Use the scheduler name in the first profile to record leader election.
		coreRecorder := c.EventBroadcaster.DeprecatedNewLegacyRecorder(DefaultLeaderElectionName)
		leaderElectionConfig, err = makeLeaderElectionConfig(c.BinderConfig.LeaderElection, leaderElectionClient, coreRecorder)
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
		Name:          DefaultLeaderElectionName,
	}, nil
}

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

	client, err := clientset.NewForConfig(restclient.AddUserAgent(kubeConfig, "binder"))
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
	// TODO make config struct use int instead of int32?
	crdKubeConfig.Burst = int(config.Burst)

	godelCrdClient, err := godelclient.NewForConfig(restclient.AddUserAgent(crdKubeConfig, "binder"))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	katalystCrdClient, err := katalystclient.NewForConfig(restclient.AddUserAgent(crdKubeConfig, "binder"))
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}

	return client, leaderElectionClient, eventClient, godelCrdClient, katalystCrdClient, nil
}
