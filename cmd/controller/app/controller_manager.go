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

package app

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	goruntime "runtime"
	"time"

	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	apiserver "k8s.io/apiserver/pkg/server"
	genericfilters "k8s.io/apiserver/pkg/server/filters"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/apiserver/pkg/server/routes"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/cli/globalflag"
	componentbaseconfig "k8s.io/component-base/config"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/term"
	"k8s.io/klog/v2"

	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	controllerappconfig "github.com/kubewharf/godel-scheduler/cmd/controller/app/config"
	"github.com/kubewharf/godel-scheduler/cmd/controller/app/options"
	"github.com/kubewharf/godel-scheduler/cmd/scheduler/app/util/configz"
	"github.com/kubewharf/godel-scheduler/pkg/controller"
	godelctrlmgrconfig "github.com/kubewharf/godel-scheduler/pkg/controller/apis/config"
	clientbuilder "github.com/kubewharf/godel-scheduler/pkg/controller/clientbuilder"
	controllerhealthz "github.com/kubewharf/godel-scheduler/pkg/controller/healthz"
	leadermigration "github.com/kubewharf/godel-scheduler/pkg/controller/leadermigration"
	controllersmetrics "github.com/kubewharf/godel-scheduler/pkg/controller/metrics"
	routeutil "github.com/kubewharf/godel-scheduler/pkg/util/route"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
	"github.com/kubewharf/godel-scheduler/pkg/version/verflag"
)

const (
	ComponentName = "godel-controller-manager"
	// ControllerStartJitter is the Jitter used when starting controller managers
	ControllerStartJitter = 1.0
	ConfigzName           = "godel-controller-manager-config"
)

var ControllersDisabledByDefault = sets.NewString()

func NewGodelControllerCmd() *cobra.Command {
	opts, err := options.NewGodelControllerManagerOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to initialize command options: %v\n", err)
		os.Exit(1)
	}

	godelControllerCmd := &cobra.Command{
		Use:   ComponentName,
		Short: "manager for godel controllers such as podgroup,reservation...",
		Long:  `manager for godel controllers such as podgroup, reservation...`,
		// Uncomment the following line if your bare application
		// has an action associated with it:
		Run: func(cmd *cobra.Command, args []string) {
			if err := runCommand(cmd, opts, args); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}

	fs := godelControllerCmd.Flags()
	namedFlagSets := opts.Flags(KnownControllers(), ControllersDisabledByDefault.List())
	globalflag.AddGlobalFlags(namedFlagSets.FlagSet("global"), godelControllerCmd.Name())
	verflag.AddFlags(namedFlagSets.FlagSet("global"))
	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}

	usageFmt := "Usage:\n  %s\n"
	cols, _, _ := term.TerminalSize(godelControllerCmd.OutOrStdout())
	godelControllerCmd.SetUsageFunc(func(cmd *cobra.Command) error {
		fmt.Fprintf(cmd.OutOrStderr(), usageFmt, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStderr(), namedFlagSets, cols)
		return nil
	})
	godelControllerCmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n"+usageFmt, cmd.Long, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStdout(), namedFlagSets, cols)
	})

	return godelControllerCmd
}

func runCommand(cmd *cobra.Command, opts *options.GodelControllerManagerOptions, args []string) error {
	verflag.PrintAndExitIfRequested()
	initKlogV2WithV1Flags(cmd.Flags())
	if len(args) != 0 {
		fmt.Fprint(os.Stderr, "arguments are not supported\n")
	}

	if err := opts.Validate(KnownControllers(), ControllersDisabledByDefault.List()); err != nil {
		return err
	}

	if len(opts.WriteConfigTo) > 0 {
		c := &controllerappconfig.Config{}
		if err := opts.ApplyTo(c); err != nil {
			return err
		}
		klog.V(1).InfoS("Wrote configuration to: ", "configPath", opts.WriteConfigTo)
		return nil
	}

	c, err := opts.Config(KnownControllers(), ControllersDisabledByDefault.List())
	if err != nil {
		return err
	}

	// TODO: add feature enablement metrics

	// Get the completed config
	cc := c.Complete()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	return Run(ctx, cc)
}

func Run(ctx context.Context, cc *controllerappconfig.CompletedConfig) error {
	// Start events processing pipeline.
	cc.EventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: cc.Client.CoreV1().Events("")})
	defer cc.EventBroadcaster.Shutdown()

	if cfgz, err := configz.New(ConfigzName); err == nil {
		cfgz.Set(cc.ComponentConfig)
	} else {
		klog.ErrorS(err, "Unable to register configz")
	}
	// Setup healthz checks.
	var checks []healthz.HealthChecker
	var electionChecker *leaderelection.HealthzAdaptor
	if cc.ComponentConfig.Generic.LeaderElection.LeaderElect {
		electionChecker = leaderelection.NewLeaderHealthzAdaptor(time.Second * 20)
		checks = append(checks, electionChecker)
	}
	healthzHandler := controllerhealthz.NewMutableHealthzHandler(checks...)

	// Start the controller manager HTTP server
	// unsecuredMux is the handler for these controller *after* authn/authz filters have been applied
	var unsecuredMux *mux.PathRecorderMux
	if cc.SecureServing != nil {
		unsecuredMux = newBaseHandler(&cc.ComponentConfig.Generic.Debugging, healthzHandler)
		handler := buildHandlerChain(unsecuredMux, &cc.Authorization, &cc.Authentication)
		// TODO: handle stoppedCh and listenerStoppedCh returned by c.SecureServing.Serve
		if _, _, err := cc.SecureServing.Serve(handler, 0, ctx.Done()); err != nil {
			return err
		}
	}

	if cc.InsecureServing != nil {
		handler := buildHandlerChain(newBaseHandler(&cc.ComponentConfig.Generic.Debugging, healthzHandler), nil, nil)
		if err := cc.InsecureServing.Serve(handler, 0, ctx.Done()); err != nil {
			return fmt.Errorf("failed to start healthz server: %v", err)
		}
	}

	clientBuilder, rootClientBuilder, godelClientBuilder := createClientBuilders(cc)

	run := func(ctx context.Context, initializersFunc ControllerInitializersFunc) {
		controllerContext, err := CreateControllerContext(cc, rootClientBuilder, clientBuilder, godelClientBuilder, ctx.Done())
		if err != nil {
			klog.ErrorS(err, "Error building controller context")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}
		controllerInitializers := initializersFunc()
		if err := StartControllers(ctx, controllerContext, controllerInitializers, unsecuredMux, healthzHandler); err != nil {
			klog.ErrorS(err, "Error starting controllers")
			klog.FlushAndExit(klog.ExitFlushTimeout, 1)
		}

		controllerContext.InformerFactory.Start(ctx.Done())
		controllerContext.GodelInformerFactory.Start(ctx.Done())
		close(controllerContext.InformersStarted)

		closer := tracing.NewTracer(
			ComponentName,
			cc.ComponentConfig.Tracer)
		defer closer.Close()

		<-ctx.Done()
	}

	// No leader election, run directly
	if !cc.ComponentConfig.Generic.LeaderElection.LeaderElect {
		run(ctx, NewControllerInitializers)
		return nil
	}

	id, err := os.Hostname()
	if err != nil {
		return err
	}

	// add a uniquifier so that two processes on the same host don't accidentally both become active
	id = id + "_" + string(uuid.NewUUID())

	// leaderMigrator will be non-nil if and only if Leader Migration is enabled.
	var leaderMigrator *leadermigration.LeaderMigrator = nil

	// If leader migration is enabled, create the LeaderMigrator and prepare for migration
	if leadermigration.Enabled(cc.ComponentConfig.Generic) {
		klog.V(4).InfoS("starting leader migration")

		leaderMigrator = leadermigration.NewLeaderMigrator(&cc.ComponentConfig.Generic.LeaderMigration,
			"godel-controller-manager")

		// FIXME:Wrap saTokenControllerInitFunc to signal readiness for migration after starting
		//  the controller.
	}

	// shallow copy, do not modify the kubeConfig.Timeout.
	restConfig := *cc.Kubeconfig
	restConfig.Timeout = cc.ComponentConfig.Generic.LeaderElection.RenewDeadline.Duration
	leaderElectionClient, err := clientset.NewForConfig(restclient.AddUserAgent(&restConfig, "godel-controller-manager-leader-election"))
	if err != nil {
		return err
	}

	// Start the main lock
	// nolint: byted_goroutine_recover , no recover
	go leaderElectAndRun(ctx, cc, id, electionChecker,
		cc.ComponentConfig.Generic.LeaderElection.ResourceLock,
		cc.ComponentConfig.Generic.LeaderElection.ResourceName,
		leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				initializersFunc := NewControllerInitializers
				if leaderMigrator != nil {
					// If leader migration is enabled, we should start only non-migrated controllers
					//  for the main lock.
					initializersFunc = createInitializersFunc(leaderMigrator.FilterFunc, leadermigration.ControllerNonMigrated)
					klog.V(5).InfoS("leader migration: starting main controllers.")
				}
				run(ctx, initializersFunc)
			},
			OnStoppedLeading: func() {
				klog.ErrorS(nil, "leaderelection lost")
				klog.FlushAndExit(klog.ExitFlushTimeout, 1)
			},
		},
		leaderElectionClient,
	)

	// If Leader Migration is enabled, proceed to attempt the migration lock.
	if leaderMigrator != nil {
		// Wait for Service Account Token Controller to start before acquiring the migration lock.
		// At this point, the main lock must have already been acquired, or the KCM process already exited.
		// We wait for the main lock before acquiring the migration lock to prevent the situation
		//  where KCM instance A holds the main lock while KCM instance B holds the migration lock.
		<-leaderMigrator.MigrationReady

		// Start the migration lock.
		// nolint: byted_goroutine_recover , no recover
		go leaderElectAndRun(ctx, cc, id, electionChecker,
			cc.ComponentConfig.Generic.LeaderMigration.ResourceLock,
			cc.ComponentConfig.Generic.LeaderMigration.LeaderName,
			leaderelection.LeaderCallbacks{
				OnStartedLeading: func(ctx context.Context) {
					klog.V(4).InfoS("leader migration: starting migrated controllers.")
					run(ctx, createInitializersFunc(leaderMigrator.FilterFunc, leadermigration.ControllerMigrated))
				},
				OnStoppedLeading: func() {
					klog.ErrorS(nil, "migration leaderelection lost")
					klog.FlushAndExit(klog.ExitFlushTimeout, 1)
				},
			},
			leaderElectionClient,
		)
	}

	<-ctx.Done()
	return nil
}

// ControllerContext defines the context object for controller
type ControllerContext struct {
	// ClientBuilder will provide a client for this controller to use
	ClientBuilder      clientbuilder.ControllerClientBuilder
	GodelClientBuilder clientbuilder.GodelClientBuilder

	// InformerFactory gives access to informers for the controller.
	InformerFactory informers.SharedInformerFactory

	// godel Informer factory
	GodelInformerFactory crdinformers.SharedInformerFactory

	// TODO: ObjectOrMetadataInformerFactory gives access to informers for typed resources
	// and dynamic resources by their metadata. All generic controllers currently use
	// object metadata - if a future controller needs access to the full object this
	// would become GenericInformerFactory and take a dynamic client.
	// ObjectOrMetadataInformerFactory informerfactory.InformerFactory

	// ComponentConfig provides access to init options for a given controller
	ComponentConfig *godelctrlmgrconfig.GodelControllerManagerConfiguration

	// TODO: DeferredDiscoveryRESTMapper is a RESTMapper that will defer
	// initialization of the RESTMapper until the first mapping is
	// requested.

	// AvailableResources is a map listing currently available resources
	AvailableResources map[schema.GroupVersionResource]bool

	// InformersStarted is closed after all of the controllers have been initialized and are running.  After this point it is safe,
	// for an individual controller to start the shared informers. Before it is closed, they should not.
	InformersStarted chan struct{}

	// ResyncPeriod generates a duration each time it is invoked; this is so that
	// multiple controllers don't get into lock-step and all hammer the apiserver
	// with list requests simultaneously.
	ResyncPeriod func() time.Duration

	// ControllerManagerMetrics provides a proxy to set controller manager specific metrics.
	ControllerManagerMetrics *controllersmetrics.ControllerManagerMetrics
}

// IsControllerEnabled checks if the context's controllers enabled or not
func (c ControllerContext) IsControllerEnabled(name string) bool {
	return isControllerEnabled(name, ControllersDisabledByDefault, c.ComponentConfig.Generic.Controllers)
}

// InitFunc is used to launch a particular controller. It returns a controller
// that can optionally implement other interfaces so that the controller manager
// can support the requested features.
// The returned controller may be nil, which will be considered an anonymous controller
// that requests no additional features from the controller manager.
// Any error returned will cause the controller process to `Fatal`
// The bool indicates whether the controller was enabled.
type InitFunc func(ctx context.Context, controllerCtx ControllerContext) (controller controller.Interface, enabled bool, err error)

// ControllerInitializersFunc is used to create a collection of initializers
// given the loopMode.
type ControllerInitializersFunc func() (initializers map[string]InitFunc)

var _ ControllerInitializersFunc = NewControllerInitializers

// KnownControllers returns all known controllers' name
func KnownControllers() []string {
	ret := sets.StringKeySet(NewControllerInitializers())

	return ret.List()
}

// leaderElectAndRun runs the leader election, and runs the callbacks once the leader lease is acquired.
// nolint: byted_goroutine_recover , no recover
func leaderElectAndRun(ctx context.Context, c *controllerappconfig.CompletedConfig, lockIdentity string, electionChecker *leaderelection.HealthzAdaptor, resourceLock string, leaseName string, callbacks leaderelection.LeaderCallbacks, client *clientset.Clientset) {
	rl, err := resourcelock.New(resourceLock,
		c.ComponentConfig.Generic.LeaderElection.ResourceNamespace,
		leaseName,
		client.CoreV1(),
		client.CoordinationV1(),
		resourcelock.ResourceLockConfig{
			Identity:      lockIdentity,
			EventRecorder: c.EventRecorder,
		})
	if err != nil {
		klog.ErrorS(err, "couldn't create resource lock")
		klog.FlushAndExit(klog.ExitFlushTimeout, 1)
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:          rl,
		LeaseDuration: c.ComponentConfig.Generic.LeaderElection.LeaseDuration.Duration,
		RenewDeadline: c.ComponentConfig.Generic.LeaderElection.RenewDeadline.Duration,
		RetryPeriod:   c.ComponentConfig.Generic.LeaderElection.RetryPeriod.Duration,
		Callbacks:     callbacks,
		WatchDog:      electionChecker,
		Name:          leaseName,
	})

	panic("unreachable")
}

// createInitializersFunc creates a initializersFunc that returns all initializer
// with expected as the result after filtering through filterFunc.
func createInitializersFunc(filterFunc leadermigration.FilterFunc, expected leadermigration.FilterResult) ControllerInitializersFunc {
	return func() map[string]InitFunc {
		initializers := make(map[string]InitFunc)
		for name, initializer := range NewControllerInitializers() {
			if filterFunc(name) == expected {
				initializers[name] = initializer
			}
		}
		return initializers
	}
}

// NewControllerInitializers is a public map of named controller groups (you can start more than one in an init func)
// paired to their InitFunc.  This allows for structured downstream composition and subdivision.
func NewControllerInitializers() map[string]InitFunc {
	controllers := map[string]InitFunc{}

	// All of the controllers must have unique names, or else we will explode.
	register := func(name string, fn InitFunc) {
		if _, found := controllers[name]; found {
			panic(fmt.Sprintf("controller name %q was registered twice", name))
		}
		controllers[name] = fn
	}

	register("reservation", startReservationController)

	return controllers
}

// buildHandlerChain builds a handler chain with a base handler and CompletedConfig.
func buildHandlerChain(apiHandler http.Handler, authorizationInfo *apiserver.AuthorizationInfo, authenticationInfo *apiserver.AuthenticationInfo) http.Handler {
	requestInfoResolver := &apirequest.RequestInfoFactory{}

	handler := apiHandler
	handler = genericapifilters.WithRequestInfo(handler, requestInfoResolver)
	handler = genericapifilters.WithCacheControl(handler)
	handler = genericfilters.WithPanicRecovery(handler, requestInfoResolver)

	return handler
}

// newBaseHandler takes in CompletedConfig and returns a handler.
func newBaseHandler(c *componentbaseconfig.DebuggingConfiguration, healthzHandler http.Handler) *mux.PathRecorderMux {
	mux := mux.NewPathRecorderMux("controller-manager")
	mux.Handle("/healthz", healthzHandler)
	if c.EnableProfiling {
		routes.Profiling{}.Install(mux)
		if c.EnableContentionProfiling {
			goruntime.SetBlockProfileRate(1)
		}
		routes.DebugFlags{}.Install(mux, "v", routeutil.StringFlagHandler(routeutil.GlogSetter, routeutil.GlogGetter))
	}
	configz.InstallHandler(mux)
	installMetricHandler(mux)

	return mux
}

// createClientBuilders creates clientBuilder and rootClientBuilder from the given configuration
func createClientBuilders(c *controllerappconfig.CompletedConfig) (clientBuilder clientbuilder.ControllerClientBuilder, rootClientBuilder clientbuilder.ControllerClientBuilder, godelClientBuilder clientbuilder.GodelClientBuilder) {
	rootClientBuilder = clientbuilder.SimpleControllerClientBuilder{
		ClientConfig: c.Kubeconfig,
	}

	godelClientBuilder = clientbuilder.SimpleGodelClientBuilder{
		ClientConfig: c.CrdKubeconfig,
	}

	clientBuilder = rootClientBuilder
	return
}

// CreateControllerContext creates a context struct containing references to resources needed by the
// controllers such as the cloud provider and clientBuilder. rootClientBuilder is only used for
// the shared-informers client and token controller.
func CreateControllerContext(
	s *controllerappconfig.CompletedConfig,
	rootClientBuilder, clientBuilder clientbuilder.ControllerClientBuilder,
	godelClientBuilder clientbuilder.GodelClientBuilder,
	stop <-chan struct{},
) (ControllerContext, error) {
	versionedClient := rootClientBuilder.ClientOrDie("shared-informers")
	godelClient := godelClientBuilder.ClientOrDie("shared-informers")

	sharedInformers := informers.NewSharedInformerFactory(versionedClient, ResyncPeriod(s)())
	godelInformers := crdinformers.NewSharedInformerFactory(godelClient, ResyncPeriod(s)())

	// If apiserver is not running we should wait for some time and fail only then. This is particularly
	// important when we start apiserver and controller manager at the same time.
	if err := waitForAPIServer(versionedClient, 10*time.Second); err != nil {
		return ControllerContext{}, fmt.Errorf("failed to wait for apiserver being healthy: %v", err)
	}

	availableResources, err := GetAvailableResources(rootClientBuilder)
	if err != nil {
		return ControllerContext{}, err
	}

	ctx := ControllerContext{
		ClientBuilder:        clientBuilder,
		GodelClientBuilder:   godelClientBuilder,
		InformerFactory:      sharedInformers,
		GodelInformerFactory: godelInformers,
		ComponentConfig:      s.ComponentConfig,
		AvailableResources:   availableResources,
		InformersStarted:     make(chan struct{}),
		ResyncPeriod:         ResyncPeriod(s),

		ControllerManagerMetrics: controllersmetrics.NewControllerManagerMetrics(ComponentName),
	}
	controllersmetrics.Register()
	return ctx, nil
}

// StartControllers starts a set of controllers with a specified ControllerContext
func StartControllers(ctx context.Context, controllerCtx ControllerContext, controllers map[string]InitFunc,
	unsecuredMux *mux.PathRecorderMux, healthzHandler *controllerhealthz.MutableHealthzHandler,
) error {
	var controllerChecks []healthz.HealthChecker

	// Each controller is passed a context where the logger has the name of
	// the controller set through WithName. That name then becomes the prefix
	// of all log messages emitted by that controller.
	//
	// In this loop, an explicit "controller" key is used instead, for two reasons:
	// - while contextual logging is alpha, klog.LoggerWithName is still a no-op,
	//   so we cannot rely on it yet to add the name
	// - it allows distinguishing between log entries emitted by the controller
	//   and those emitted for it - this is a bit debatable and could be revised.
	for controllerName, initFn := range controllers {
		if !controllerCtx.IsControllerEnabled(controllerName) {
			klog.V(4).InfoS("Warning: controller is disabled", "controller", controllerName)
			continue
		}

		time.Sleep(wait.Jitter(controllerCtx.ComponentConfig.Generic.ControllerStartInterval.Duration, ControllerStartJitter))

		klog.V(1).InfoS("Starting controller", "controller", controllerName)
		// TODO: pass controller name to ctx
		ctrl, started, err := initFn(ctx, controllerCtx)
		if err != nil {
			klog.ErrorS(err, "Error starting controller", "controller", controllerName)
			return err
		}

		if !started {
			klog.InfoS("Warning: skipping controller", "controller", controllerName)
			continue
		}
		check := controllerhealthz.NamedPingChecker(controllerName)
		if ctrl != nil {
			// check if the controller supports and requests a debugHandler
			// and it needs the unsecuredMux to mount the handler onto.
			if debuggable, ok := ctrl.(controller.Debuggable); ok && unsecuredMux != nil {
				if debugHandler := debuggable.DebuggingHandler(); debugHandler != nil {
					basePath := "/debug/controllers/" + controllerName
					unsecuredMux.UnlistedHandle(basePath, http.StripPrefix(basePath, debugHandler))
					unsecuredMux.UnlistedHandlePrefix(basePath+"/", http.StripPrefix(basePath, debugHandler))
				}
			}
			if healthCheckable, ok := ctrl.(controller.HealthCheckable); ok {
				if realCheck := healthCheckable.HealthChecker(); realCheck != nil {
					check = controllerhealthz.NamedHealthChecker(controllerName, realCheck)
				}
			}
		}
		controllerChecks = append(controllerChecks, check)

		klog.InfoS("Started controller", "controller", controllerName)
	}

	healthzHandler.AddHealthChecker(controllerChecks...)

	return nil
}

func installMetricHandler(pathRecorderMux *mux.PathRecorderMux) {
	//lint:ignore SA1019 See the Metrics Stability Migration KEP
	defaultMetricsHandler := legacyregistry.Handler().ServeHTTP
	pathRecorderMux.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		if req.Method == "DELETE" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			io.WriteString(w, "metrics reset\n")
			return
		}
		defaultMetricsHandler(w, req)
	})
}

// ResyncPeriod returns a function which generates a duration each time it is
// invoked; this is so that multiple controllers don't get into lock-step and all
// hammer the apiserver with list requests simultaneously.
func ResyncPeriod(c *controllerappconfig.CompletedConfig) func() time.Duration {
	return func() time.Duration {
		factor := rand.Float64() + 1
		return time.Duration(float64(c.ComponentConfig.Generic.MinResyncPeriod.Nanoseconds()) * factor)
	}
}

// GetAvailableResources gets the map which contains all available resources of the apiserver
// TODO: In general, any controller checking this needs to be dynamic so
// users don't have to restart their controller manager if they change the apiserver.
// Until we get there, the structure here needs to be exposed for the construction of a proper ControllerContext.
func GetAvailableResources(clientBuilder clientbuilder.ControllerClientBuilder) (map[schema.GroupVersionResource]bool, error) {
	client := clientBuilder.ClientOrDie("controller-discovery")
	discoveryClient := client.Discovery()
	_, resourceMap, err := discoveryClient.ServerGroupsAndResources()
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("unable to get all supported resources from server: %v", err))
	}
	if len(resourceMap) == 0 {
		return nil, fmt.Errorf("unable to get any supported resources from server")
	}

	allResources := map[schema.GroupVersionResource]bool{}
	for _, apiResourceList := range resourceMap {
		version, err := schema.ParseGroupVersion(apiResourceList.GroupVersion)
		if err != nil {
			return nil, err
		}
		for _, apiResource := range apiResourceList.APIResources {
			allResources[version.WithResource(apiResource.Name)] = true
		}
	}

	return allResources, nil
}
