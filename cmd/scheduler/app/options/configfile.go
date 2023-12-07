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
	"io/ioutil"
	"os"

	godelschedulerconfig "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config"
	godelschedulerscheme "github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/scheme"
	"github.com/kubewharf/godel-scheduler/pkg/scheduler/apis/config/v1beta1"

	"k8s.io/apimachinery/pkg/runtime"
)

func loadConfigFromFile(file string) (*godelschedulerconfig.GodelSchedulerConfiguration, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	return loadConfig(data)
}

func loadConfig(data []byte) (*godelschedulerconfig.GodelSchedulerConfiguration, error) {
	// The UniversalDecoder runs defaulting and returns the internal type by default.
	obj, gvk, err := godelschedulerscheme.Codecs.UniversalDecoder().Decode(data, nil, nil)
	if err != nil {
		return nil, err
	}
	if cfgObj, ok := obj.(*godelschedulerconfig.GodelSchedulerConfiguration); ok {
		cfgObj.TypeMeta.APIVersion = gvk.GroupVersion().String()
		switch cfgObj.TypeMeta.APIVersion {
		case v1beta1.SchemeGroupVersion.String():
			fmt.Printf("GodelSchedulerConfiguration v1beta1 is loaded.\n")
		}
		return cfgObj, nil
	}
	return nil, fmt.Errorf("couldn't decode as GodelSchedulerConfiguration, got %s: ", gvk)
}

// WriteConfigFile writes the config into the given file name as YAML.
func WriteConfigFile(fileName string, cfg *godelschedulerconfig.GodelSchedulerConfiguration) error {
	const mediaType = runtime.ContentTypeYAML
	info, ok := runtime.SerializerInfoForMediaType(godelschedulerscheme.Codecs.SupportedMediaTypes(), mediaType)
	if !ok {
		return fmt.Errorf("unable to locate encoder -- %q is not a supported media type", mediaType)
	}

	encoder := godelschedulerscheme.Codecs.EncoderForVersion(info.Serializer, godelschedulerconfig.SchemeGroupVersion)

	configFile, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer configFile.Close()
	if err := encoder.Encode(cfg, configFile); err != nil {
		return err
	}

	return nil
}
