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
	"flag"

	"github.com/spf13/pflag"
	"k8s.io/component-base/logs"
	klogv2 "k8s.io/klog/v2"
)

func initKlogV2WithV1Flags(cmdFlags *pflag.FlagSet) {
	var klogv2Flags flag.FlagSet
	klogv2.InitFlags(&klogv2Flags)

	cmdFlags.VisitAll(func(f *pflag.Flag) {
		klogv2Flags.Set(f.Name, f.Value.String())
	})

	klogv2.SetOutput(logs.KlogWriter{})

	return
}
