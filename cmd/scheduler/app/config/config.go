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

package config

import (
	godelclient "github.com/kubewharf/godel-scheduler-api/pkg/client/clientset/versioned"
	crdinformers "github.com/kubewharf/godel-scheduler-api/pkg/client/informers/externalversions"
	katalystclient "github.com/kubewharf/katalyst-api/pkg/client/clientset/versioned"
	katalystinformers "github.com/kubewharf/katalyst-api/pkg/client/informers/externalversions"
	apiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/client-go/informers"
	clientset "k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"

	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	cmdutil "github.com/kubewharf/godel-scheduler/pkg/util/cmd"
)

type Config struct {
	// config is the scheduler server's configuration object.
	ComponentConfig config.GodelSchedulerConfiguration

	Client          clientset.Interface
	InformerFactory informers.SharedInformerFactory

	// godel crd client & informer
	GodelCrdClient          godelclient.Interface
	GodelCrdInformerFactory crdinformers.SharedInformerFactory

	KatalystCrdClient          katalystclient.Interface
	KatalystCrdInformerFactory katalystinformers.SharedInformerFactory

	// LoopbackClientConfig is a config for a privileged loopback connection
	LoopbackClientConfig *restclient.Config

	InsecureServing        *apiserver.DeprecatedInsecureServingInfo // nil will disable serving on an insecure port
	InsecureMetricsServing *apiserver.DeprecatedInsecureServingInfo // non-nil if metrics should be served independentl
	SecureServing          *apiserver.SecureServingInfo
	Authentication         apiserver.AuthenticationInfo
	Authorization          apiserver.AuthorizationInfo

	// LeaderElection is optional.
	LeaderElection *leaderelection.LeaderElectionConfig

	// EventBroadcaster is wrapper for event broadcaster, compatible with core.v1.Event and events.v1beta1.Event, used for Events.
	// It will be removed once the migration for events from core API to events API is done.
	// More details can be found at https://github.com/kubernetes/enhancements/blob/master/keps/sig-instrumentation/383-new-event-api-ga-graduation/README.md
	EventBroadcaster cmdutil.EventBroadcasterAdapter
}

type completedConfig struct {
	*Config
}

type CompletedConfig struct {
	*completedConfig
}

func (c *Config) Complete() CompletedConfig {
	cc := completedConfig{c}
	if c.InsecureServing != nil {
		c.InsecureServing.Name = "healthz"
	}
	if c.InsecureMetricsServing != nil {
		c.InsecureMetricsServing.Name = "metrics"
	}

	apiserver.AuthorizeClientBearerToken(c.LoopbackClientConfig, &c.Authentication, &c.Authorization)
	return CompletedConfig{&cc}
}
