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
	"net/http"
	"os"
	goruntime "runtime"

	"github.com/spf13/cobra"

	"github.com/kubewharf/godel-scheduler/cmd/dispatcher/app/config"
	"github.com/kubewharf/godel-scheduler/cmd/dispatcher/app/options"
	"github.com/kubewharf/godel-scheduler/cmd/scheduler/app/util/configz"
	"github.com/kubewharf/godel-scheduler/pkg/dispatcher"
	godeldispatcherconfig "github.com/kubewharf/godel-scheduler/pkg/dispatcher/config"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
	routeutil "github.com/kubewharf/godel-scheduler/pkg/util/route"
	"github.com/kubewharf/godel-scheduler/pkg/util/tracing"
	"github.com/kubewharf/godel-scheduler/pkg/version/verflag"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	apirequest "k8s.io/apiserver/pkg/endpoints/request"
	genericfilters "k8s.io/apiserver/pkg/server/filters"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/apiserver/pkg/server/mux"
	"k8s.io/apiserver/pkg/server/routes"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/tools/leaderelection"
	cliflag "k8s.io/component-base/cli/flag"
	"k8s.io/component-base/cli/globalflag"
	"k8s.io/component-base/metrics/legacyregistry"
	"k8s.io/component-base/term"
	"k8s.io/klog/v2"
)

const ComponentName = "dispatcher"

func NewDispatcherCommand() *cobra.Command {
	opts, err := options.NewOptions()
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to initialize command options: %v\n", err)
		os.Exit(1)
	}
	cmd := &cobra.Command{
		Use: ComponentName,
		Run: func(cmd *cobra.Command, args []string) {
			if err := runCommand(cmd, opts, args); err != nil {
				fmt.Fprintf(os.Stderr, "%v\n", err)
				os.Exit(1)
			}
		},
	}
	fs := cmd.Flags()
	namedFlagSets := opts.Flags()
	globalflag.AddGlobalFlags(namedFlagSets.FlagSet("global"), cmd.Name())
	verflag.AddFlags(namedFlagSets.FlagSet("global"))
	for _, f := range namedFlagSets.FlagSets {
		fs.AddFlagSet(f)
	}

	usageFmt := "Usage:\n  %s\n"
	cols, _, _ := term.TerminalSize(cmd.OutOrStdout())
	cmd.SetUsageFunc(func(cmd *cobra.Command) error {
		fmt.Fprintf(cmd.OutOrStderr(), usageFmt, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStderr(), namedFlagSets, cols)
		return nil
	})
	cmd.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "%s\n\n"+usageFmt, cmd.Long, cmd.UseLine())
		cliflag.PrintSections(cmd.OutOrStdout(), namedFlagSets, cols)
	})
	cmd.MarkFlagFilename("config", "yaml", "yml", "json")

	return cmd
}

func runCommand(cmd *cobra.Command, opts *options.Options, args []string) error {
	cmdutil.InitKlogV2WithV1Flags(cmd.Flags())
	verflag.PrintAndExitIfRequested()
	if len(args) != 0 {
		fmt.Fprint(os.Stderr, "arguments are not supported\n")
	}

	if errs := opts.Validate(); len(errs) > 0 {
		return utilerrors.NewAggregate(errs)
	}

	if len(opts.WriteConfigTo) > 0 {
		c := &config.Config{}
		if err := opts.ApplyTo(c); err != nil {
			return err
		}
		klog.V(1).InfoS("Wrote configuration", "file", opts.WriteConfigTo)
		return nil
	}

	c, err := opts.Config()
	if err != nil {
		return err
	}
	// Get the completed config
	cc := c.Complete()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return Run(ctx, cc)
}

func Run(ctx context.Context, cc config.CompletedConfig) error {
	err := cc.DispatcherConfig.Tracer.Validate()
	if err != nil {
		return err
	}

	dispatcher := dispatcher.New(
		ctx.Done(),
		cc.Client,
		cc.GodelCrdClient,
		cc.InformerFactory.Core().V1().Pods(),
		cc.InformerFactory.Core().V1().Nodes(),
		cc.GodelCrdInformerFactory.Scheduling().V1alpha1().Schedulers(),
		cc.GodelCrdInformerFactory.Node().V1alpha1().NMNodes(),
		cc.GodelCrdInformerFactory.Scheduling().V1alpha1().PodGroups(),
		cc.InformerFactory.Scheduling().V1().PriorityClasses(),
		*cc.DispatcherConfig.SchedulerName,
		getEventRecorder(&cc),
	)

	// Prepare the event broadcaster.
	cc.EventBroadcaster.StartRecordingToSink(ctx.Done())

	// Setup healthz checks.
	var checks []healthz.HealthChecker
	if *cc.DispatcherConfig.LeaderElection.LeaderElect {
		checks = append(checks, cc.LeaderElection.WatchDog)
	}

	// Start up the healthz server.
	if cc.InsecureServing != nil {
		separateMetrics := cc.InsecureMetricsServing != nil
		handler := buildHandlerChain(newHealthzHandler(&cc.DispatcherConfig, separateMetrics, checks...), nil, nil)
		if err := cc.InsecureServing.Serve(handler, 0, ctx.Done()); err != nil {
			return fmt.Errorf("failed to start healthz server: %v", err)
		}
	}
	if cc.InsecureMetricsServing != nil {
		handler := buildHandlerChain(newMetricsHandler(&cc.DispatcherConfig), nil, nil)
		if err := cc.InsecureMetricsServing.Serve(handler, 0, ctx.Done()); err != nil {
			return fmt.Errorf("failed to start metrics server: %v", err)
		}
	}

	cc.InformerFactory.Start(ctx.Done())
	cc.InformerFactory.WaitForCacheSync(ctx.Done())
	cc.GodelCrdInformerFactory.Start(ctx.Done())
	cc.GodelCrdInformerFactory.WaitForCacheSync(ctx.Done())

	// Prepare a reusable runCommand function.
	run := func(ctx context.Context) {
		// Register the tracer when the dispatcher is ready.
		closer := tracing.NewTracer(
			ComponentName,
			cc.DispatcherConfig.Tracer)
		defer closer.Close()

		dispatcher.Run(ctx)
		<-ctx.Done()
	}

	// If leader election is enabled, runCommand via LeaderElector until done and exit.
	if cc.LeaderElection != nil {
		cc.LeaderElection.Callbacks = leaderelection.LeaderCallbacks{
			OnStartedLeading: run,
			OnStoppedLeading: func() {
				select {
				case <-ctx.Done():
					// We were asked to terminate. Exit 0.
					klog.InfoS("Requested to terminate. Exiting")
					klog.FlushAndExit(klog.ExitFlushTimeout, 0)
				default:
					// We lost the lock.
					klog.ErrorS(nil, "Lost leader election")
					klog.FlushAndExit(klog.ExitFlushTimeout, 1)
				}
			},
		}
		leaderElector, err := leaderelection.NewLeaderElector(*cc.LeaderElection)
		if err != nil {
			return fmt.Errorf("couldn't create leader elector: %v", err)
		}

		leaderElector.Run(ctx)

		return fmt.Errorf("lost lease")
	}

	run(ctx)
	return fmt.Errorf("finished without leader elect")
}

// buildHandlerChain wraps the given handler with the standard filters.
func buildHandlerChain(handler http.Handler, authn authenticator.Request, authz authorizer.Authorizer) http.Handler {
	requestInfoResolver := &apirequest.RequestInfoFactory{}

	handler = genericapifilters.WithRequestInfo(handler, requestInfoResolver)
	handler = genericapifilters.WithCacheControl(handler)
	handler = genericfilters.WithPanicRecovery(handler, requestInfoResolver)

	return handler
}

func installMetricHandler(pathRecorderMux *mux.PathRecorderMux) {
	configz.InstallHandler(pathRecorderMux)

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

// newMetricsHandler builds a metrics server from the config.
func newMetricsHandler(config *godeldispatcherconfig.GodelDispatcherConfiguration) http.Handler {
	pathRecorderMux := mux.NewPathRecorderMux(ComponentName)
	installMetricHandler(pathRecorderMux)
	if *config.EnableProfiling {
		routes.Profiling{}.Install(pathRecorderMux)
		if *config.EnableContentionProfiling {
			goruntime.SetBlockProfileRate(1)
		}
		routeutil.DebugFlags{}.Install(pathRecorderMux, "v", routeutil.StringFlagHandler(routeutil.GlogSetter, routeutil.GlogGetter))
	}
	return pathRecorderMux
}

// newHealthzHandler creates a healthz server from the config, and will also
// embed the metrics handler if the healthz and metrics address configurations
// are the same.
func newHealthzHandler(config *godeldispatcherconfig.GodelDispatcherConfiguration, separateMetrics bool, checks ...healthz.HealthChecker) http.Handler {
	pathRecorderMux := mux.NewPathRecorderMux(ComponentName)
	healthz.InstallHandler(pathRecorderMux, checks...)
	if !separateMetrics {
		installMetricHandler(pathRecorderMux)
	}
	if *config.EnableProfiling {
		routes.Profiling{}.Install(pathRecorderMux)
		if *config.EnableContentionProfiling {
			goruntime.SetBlockProfileRate(1)
		}
		routeutil.DebugFlags{}.Install(pathRecorderMux, "v", routeutil.StringFlagHandler(routeutil.GlogSetter, routeutil.GlogGetter))
	}
	return pathRecorderMux
}

func getEventRecorder(cc *config.CompletedConfig) events.EventRecorder {
	return cc.EventBroadcaster.NewRecorder(ComponentName)
}
