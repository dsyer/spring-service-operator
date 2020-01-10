/*

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
package controllers

import (
	api "github.com/dsyer/spring-service-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCreateConfig(t *testing.T) {
	proxy := api.ProxyService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "test",
		},
		Spec: api.ProxyServiceSpec{
			Services: []string{
				"green",
				"blue",
			},
		},
	}
	config := createConfig(proxy)
	if config.GenerateName != "demo-" {
		t.Errorf("config.GenerateName = %s; want 'demo-'", config.GenerateName)
	}
	if len(config.Data) != 0 {
		t.Errorf("config.Data = %d; want 0", len(config.Data))
	}
}

func TestCreateMap(t *testing.T) {
	proxy := api.ProxyService{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "demo",
			Namespace: "test",
		},
		Spec: api.ProxyServiceSpec{
			Services: []string{
				"green",
				"blue",
			},
		},
	}
	_, filename, _, _ := runtime.Caller(0)
	config := readMap(filepath.Dir(filename)+"/../etc", proxy)
	if len(config) != 3 {
		t.Errorf("config.Data = %d; want 3", len(config))
	}
	if !strings.Contains(config["proxy.conf"], "upstream green") {
		t.Errorf("config['proxy.conf'] = %s; want 'upstream green'", config["proxy.conf"])
	}
	if !strings.Contains(config["proxy.conf"], "server demo") {
		t.Errorf("config['proxy.conf'] = %s; want 'server green'", config["proxy.conf"])
	}
	if !strings.Contains(config["proxy.conf"], "server blue") {
		t.Errorf("config['proxy.conf'] = %s; want 'server blue'", config["proxy.conf"])
	}
	if !strings.Contains(config["proxy.conf"], "upstream blue") {
		t.Errorf("config['proxy.conf'] = %s; want 'upstream blue'", config["proxy.conf"])
	}
	for key, file := range config {
		if !strings.HasSuffix(file, "\n") {
			t.Errorf("%s; want newline", key)
		}
	}
}
