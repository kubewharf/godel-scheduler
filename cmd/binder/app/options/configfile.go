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

	godelbinderconfig "github.com/kubewharf/godel-scheduler/pkg/binder/apis/config"
	godelbinderscheme "github.com/kubewharf/godel-scheduler/pkg/binder/apis/config/scheme"
	"github.com/kubewharf/godel-scheduler/pkg/binder/apis/config/v1beta1"
)

func loadProfileFromFile(file string) (*godelbinderconfig.GodelBinderProfile, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	return loadProfile(data)
}

func loadProfile(data []byte) (*godelbinderconfig.GodelBinderProfile, error) {
	// The UniversalDecoder runs defaulting and returns the internal type by default.
	obj, gvk, err := godelbinderscheme.Codecs.UniversalDecoder().Decode(data, nil, nil)
	if err != nil {
		return nil, err
	}
	if cfgObj, ok := obj.(*godelbinderconfig.GodelBinderProfile); ok {
		return cfgObj, nil
	}
	return nil, fmt.Errorf("couldn't decode as GodelBinderProfile, got %s: ", gvk)
}

func loadConfigFromFile(file string) (*godelbinderconfig.GodelBinderConfiguration, error) {
	data, err := ioutil.ReadFile(file)
	if err != nil {
		return nil, err
	}

	return loadConfig(data)
}

func loadConfig(data []byte) (*godelbinderconfig.GodelBinderConfiguration, error) {
	// The UniversalDecoder runs defaulting and returns the internal type by default.
	obj, gvk, err := godelbinderscheme.Codecs.UniversalDecoder().Decode(data, nil, nil)
	if err != nil {
		return nil, err
	}
	if cfgObj, ok := obj.(*godelbinderconfig.GodelBinderConfiguration); ok {
		cfgObj.TypeMeta.APIVersion = gvk.GroupVersion().String()
		switch cfgObj.TypeMeta.APIVersion {
		case v1beta1.SchemeGroupVersion.String():
			fmt.Printf("GodelBinderConfiguration v1beta1 is loaded.\n")
		}

		return cfgObj, nil
	}
	return nil, fmt.Errorf("couldn't decode as GodelBinderConfiguration, got %s: ", gvk)
}
