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

	dispatcherappconfig "github.com/kubewharf/godel-scheduler/cmd/dispatcher/app/config"
	dispatcherconfig "github.com/kubewharf/godel-scheduler/pkg/dispatcher/config"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher/config/validation"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
)

const DefaultLeaderElectionName = "dispatcher"

type Options struct {
	DispatcherConfig dispatcherconfig.GodelDispatcherConfiguration

	// ConfigFile is the location of the scheduler server's configuration file.
	ConfigFile string

	// WriteConfigTo is the path where the default configuration will be written.
	WriteConfigTo string

	Master string

	CombinedInsecureServing *CombinedInsecureServingOptions
}

// NewOptions returns default dispatcher app options.
func NewOptions() (*Options, error) {
	cfg, err := newDefaultDispatcherConfig()
	if err != nil {
		return nil, err
	}

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
		DispatcherConfig: *cfg,
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

func newDefaultDispatcherConfig() (*dispatcherconfig.GodelDispatcherConfiguration, error) {
	cfg := dispatcherconfig.GodelDispatcherConfiguration{}

	dispatcherconfig.SetDefaults(&cfg)
	return &cfg, nil
}

// Flags returns flags for a specific scheduler by section name
func (o *Options) Flags() (nfs cliflag.NamedFlagSets) {
	fs := nfs.FlagSet("misc")
	fs.StringVar(&o.ConfigFile, "config", o.ConfigFile, "The path to the configuration file. Flags override values in this file.")
	fs.StringVar(&o.WriteConfigTo, "write-config-to", o.WriteConfigTo, "If set, write the configuration values to this file and exit.")
	fs.StringVar(&o.Master, "master", o.Master, "The address of the Kubernetes API server (overrides any value in kubeconfig)")
	fs.StringVar(&o.DispatcherConfig.ClientConnection.Kubeconfig, "kubeconfig", o.DispatcherConfig.ClientConnection.Kubeconfig, "path to kubeconfig file with authorization and master location information.")
	fs.Float32Var(&o.DispatcherConfig.ClientConnection.QPS, "kube-api-qps", o.DispatcherConfig.ClientConnection.QPS, "QPS to use while talking with kubernetes apiserver. This parameter is ignored if a config file is specified in --config.")
	fs.Int32Var(&o.DispatcherConfig.ClientConnection.Burst, "kube-api-burst", o.DispatcherConfig.ClientConnection.Burst, "burst to use while talking with kubernetes apiserver. This parameter is ignored if a config file is specified in --config.")
	fs.StringVar(o.DispatcherConfig.SchedulerName, "scheduler-name", *o.DispatcherConfig.SchedulerName, "components will deal with pods that pod.Spec.SchedulerName is equal to scheduler-name / is default-scheduler or empty.")
	fs.BoolVar(&o.DispatcherConfig.TakeOverDefaultScheduler, "takeover-default-scheduler", o.DispatcherConfig.TakeOverDefaultScheduler, "components will also accept pods that pod.Spec.SchedulerName == default-scheduler.")

	o.CombinedInsecureServing.AddFlags(nfs.FlagSet("insecure serving"))
	o.DispatcherConfig.Tracer.AddFlags(nfs.FlagSet("tracer"))

	BindFlags(&o.DispatcherConfig.LeaderElection, nfs.FlagSet("leader election"))
	utilfeature.DefaultMutableFeatureGate.AddFlag(nfs.FlagSet("generic"))
	return nfs
}

// ApplyTo applies the dispatcher options to the given dispatcher app configuration.
func (o *Options) ApplyTo(c *dispatcherappconfig.Config) error {
	c.DispatcherConfig = o.DispatcherConfig
	if err := o.CombinedInsecureServing.ApplyTo(c, &c.DispatcherConfig); err != nil {
		return err
	}
	return nil
}

// Validate validates all the required options.
func (o *Options) Validate() []error {
	var errs []error
	if err := validation.ValidateGodelDispatcherConfiguration(&o.DispatcherConfig).ToAggregate(); err != nil {
		errs = append(errs, err.Errors()...)
	}
	return errs
}

// Config return a scheduler config object
func (o *Options) Config() (*dispatcherappconfig.Config, error) {
	c := &dispatcherappconfig.Config{}
	if err := o.ApplyTo(c); err != nil {
		return nil, err
	}

	// Prepare kube clients.
	client, leaderElectionClient, eventClient, godelCrdClient, err := createClients(c.DispatcherConfig.ClientConnection, o.Master, c.DispatcherConfig.LeaderElection.RenewDeadline.Duration)
	if err != nil {
		return nil, err
	}

	c.EventBroadcaster = cmdutil.NewEventBroadcasterAdapter(eventClient)

	// Set up leader election if enabled.
	var leaderElectionConfig *leaderelection.LeaderElectionConfig
	if *c.DispatcherConfig.LeaderElection.LeaderElect {
		// Use the scheduler name in the first profile to record leader election.
		coreRecorder := c.EventBroadcaster.DeprecatedNewLegacyRecorder(DefaultLeaderElectionName)
		leaderElectionConfig, err = makeLeaderElectionConfig(c.DispatcherConfig.LeaderElection, leaderElectionClient, coreRecorder)
		if err != nil {
			return nil, err
		}
	}

	c.Client = client
	c.InformerFactory = cmdutil.NewInformerFactory(client, 0)
	c.GodelCrdClient = godelCrdClient

	c.GodelCrdInformerFactory = crdinformers.NewSharedInformerFactory(c.GodelCrdClient, 0)
	// TODO:(godel) delete if useless.
	// c.EventClient = eventClient.EventsV1beta1()
	// c.CoreEventClient = eventClient.CoreV1()
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

func createClients(config componentbaseconfig.ClientConnectionConfiguration, masterOverride string, timeout time.Duration) (clientset.Interface, clientset.Interface, clientset.Interface, godelclient.Interface, error) {
	if len(config.Kubeconfig) == 0 && len(masterOverride) == 0 {
		klog.InfoS("WARN: Neither --kubeconfig nor --master was specified. Using default API client. This might not work")
	}

	// This creates a client, first loading any specified kubeconfig
	// file, and then overriding the Master flag, if non-empty.
	kubeConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: config.Kubeconfig},
		&clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: masterOverride}}).ClientConfig()
	if err != nil {
		return nil, nil, nil, nil, err
	}

	kubeConfig.DisableCompression = true
	kubeConfig.AcceptContentTypes = config.AcceptContentTypes
	kubeConfig.ContentType = config.ContentType
	kubeConfig.QPS = config.QPS
	kubeConfig.Burst = int(config.Burst)

	client, err := clientset.NewForConfig(restclient.AddUserAgent(kubeConfig, "dispatcher"))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// shallow copy, do not modify the kubeConfig.Timeout.
	restConfig := *kubeConfig
	restConfig.Timeout = timeout
	leaderElectionClient, err := clientset.NewForConfig(restclient.AddUserAgent(&restConfig, "leader-election"))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	utilruntime.Must(godelclientscheme.AddToScheme(clientsetscheme.Scheme))
	eventClient, err := clientset.NewForConfig(kubeConfig)
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
	// TODO make config struct use int instead of int32?
	crdKubeConfig.Burst = int(config.Burst)

	godelCrdClient, err := godelclient.NewForConfig(restclient.AddUserAgent(crdKubeConfig, "dispatcher"))
	if err != nil {
		return nil, nil, nil, nil, err
	}

	return client, leaderElectionClient, eventClient, godelCrdClient, nil
}
