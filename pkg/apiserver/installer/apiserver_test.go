/*
Copyright 2017 The Kubernetes Authors.

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

package installer

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/emicklei/go-restful"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	genericapi "k8s.io/apiserver/pkg/endpoints"
	genericapifilters "k8s.io/apiserver/pkg/endpoints/filters"
	genericapiserver "k8s.io/apiserver/pkg/server"
	"k8s.io/metrics/pkg/apis/custom_metrics"
	installcm "k8s.io/metrics/pkg/apis/custom_metrics/install"
	cmv1beta1 "k8s.io/metrics/pkg/apis/custom_metrics/v1beta1"
	installem "k8s.io/metrics/pkg/apis/external_metrics/install"
	emv1beta1 "k8s.io/metrics/pkg/apis/external_metrics/v1beta1"

	"github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/provider"
	custommetricstorage "github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/registry/custom_metrics"
	externalmetricstorage "github.com/kubernetes-sigs/custom-metrics-apiserver/pkg/registry/external_metrics"
	sampleprovider "github.com/kubernetes-sigs/custom-metrics-apiserver/test-adapter/provider"
	"k8s.io/metrics/pkg/apis/external_metrics"
)

// defaultAPIServer exposes nested objects for testability.
type defaultAPIServer struct {
	http.Handler
	container *restful.Container
}

var (
	Scheme                      = runtime.NewScheme()
	Codecs                      = serializer.NewCodecFactory(Scheme)
	prefix                      = genericapiserver.APIGroupPrefix
	customMetricsGroupVersion   schema.GroupVersion
	customMetricsGroupInfo      genericapiserver.APIGroupInfo
	externalMetricsGroupVersion schema.GroupVersion
	externalMetricsGroupInfo    genericapiserver.APIGroupInfo
	codec                       = Codecs.LegacyCodec()
	emptySet                    = labels.Set{}
	matchingSet                 = labels.Set{"foo": "bar"}
)

func init() {
	installcm.Install(Scheme)
	installem.Install(Scheme)

	// we need custom conversion functions to list resources with options
	RegisterConversions(Scheme)

	// we need to add the options to empty v1
	// TODO fix the server code to avoid this
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})

	// TODO: keep the generic API server from wanting this
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)

	customMetricsGroupInfo = genericapiserver.NewDefaultAPIGroupInfo(custom_metrics.GroupName, Scheme, runtime.NewParameterCodec(Scheme), Codecs)
	customMetricsGroupVersion = customMetricsGroupInfo.PrioritizedVersions[0]
	externalMetricsGroupInfo = genericapiserver.NewDefaultAPIGroupInfo(external_metrics.GroupName, Scheme, runtime.NewParameterCodec(Scheme), Codecs)
	externalMetricsGroupVersion = externalMetricsGroupInfo.PrioritizedVersions[0]
}

func extractBody(response *http.Response, object runtime.Object) error {
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return err
	}
	return runtime.DecodeInto(codec, body, object)
}

func extractBodyString(response *http.Response) (string, error) {
	defer response.Body.Close()
	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	return string(body), err
}

func handleCustomMetrics(prov provider.CustomMetricsProvider) http.Handler {
	container := restful.NewContainer()
	container.Router(restful.CurlyRouter{})
	mux := container.ServeMux
	resourceStorage := custommetricstorage.NewREST(prov)
	group := &MetricsAPIGroupVersion{
		DynamicStorage: resourceStorage,
		APIGroupVersion: &genericapi.APIGroupVersion{
			Root:             prefix,
			GroupVersion:     customMetricsGroupVersion,
			MetaGroupVersion: customMetricsGroupInfo.MetaGroupVersion,

			ParameterCodec:  customMetricsGroupInfo.ParameterCodec,
			Serializer:      customMetricsGroupInfo.NegotiatedSerializer,
			Creater:         customMetricsGroupInfo.Scheme,
			Convertor:       customMetricsGroupInfo.Scheme,
			UnsafeConvertor: runtime.UnsafeObjectConvertor(customMetricsGroupInfo.Scheme),
			Typer:           customMetricsGroupInfo.Scheme,
			Linker:          runtime.SelfLinker(meta.NewAccessor()),
		},
		ResourceLister: provider.NewCustomMetricResourceLister(prov),
		Handlers:       &CMHandlers{},
	}

	if err := group.InstallREST(container); err != nil {
		panic(fmt.Sprintf("unable to install container %s: %v", group.GroupVersion, err))
	}

	var handler http.Handler = &defaultAPIServer{mux, container}
	reqInfoResolver := genericapiserver.NewRequestInfoResolver(&genericapiserver.Config{})
	handler = genericapifilters.WithRequestInfo(handler, reqInfoResolver)
	return handler
}

func handleExternalMetrics(prov provider.ExternalMetricsProvider) http.Handler {
	container := restful.NewContainer()
	container.Router(restful.CurlyRouter{})
	mux := container.ServeMux
	resourceStorage := externalmetricstorage.NewREST(prov)

	group := &MetricsAPIGroupVersion{
		DynamicStorage: resourceStorage,
		APIGroupVersion: &genericapi.APIGroupVersion{
			Root:             prefix,
			GroupVersion:     externalMetricsGroupVersion,
			MetaGroupVersion: externalMetricsGroupInfo.MetaGroupVersion,

			ParameterCodec:  externalMetricsGroupInfo.ParameterCodec,
			Serializer:      externalMetricsGroupInfo.NegotiatedSerializer,
			Creater:         externalMetricsGroupInfo.Scheme,
			Convertor:       externalMetricsGroupInfo.Scheme,
			UnsafeConvertor: runtime.UnsafeObjectConvertor(externalMetricsGroupInfo.Scheme),
			Typer:           externalMetricsGroupInfo.Scheme,
			Linker:          runtime.SelfLinker(meta.NewAccessor()),
		},
		ResourceLister: provider.NewExternalMetricResourceLister(prov),
		Handlers:       &EMHandlers{},
	}

	if err := group.InstallREST(container); err != nil {
		panic(fmt.Sprintf("unable to install container %s: %v", group.GroupVersion, err))
	}

	var handler http.Handler = &defaultAPIServer{mux, container}
	reqInfoResolver := genericapiserver.NewRequestInfoResolver(&genericapiserver.Config{})
	handler = genericapifilters.WithRequestInfo(handler, reqInfoResolver)
	return handler
}

type fakeCMProvider struct {
	rootValues             map[string][]custom_metrics.MetricValue
	namespacedValues       map[string][]custom_metrics.MetricValue
	rootSubsetCounts       map[string]int
	namespacedSubsetCounts map[string]int
	metrics                []provider.CustomMetricInfo
}

func (p *fakeCMProvider) valuesFor(name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (string, []custom_metrics.MetricValue, bool) {
	if info.Namespaced {
		metricId := name.Namespace + "/" + info.GroupResource.String() + "/" + name.Name + "/" + info.Metric
		values, ok := p.namespacedValues[metricId]
		return metricId, values, ok
	} else {
		metricId := info.GroupResource.String() + "/" + name.Name + "/" + info.Metric
		values, ok := p.rootValues[metricId]
		return metricId, values, ok
	}
}

func (p *fakeCMProvider) GetMetricByName(ctx context.Context, name types.NamespacedName, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValue, error) {
	metricId, values, ok := p.valuesFor(name, info, metricSelector)
	if !ok {
		return nil, fmt.Errorf("non-existent metric requested (id: %s)", metricId)
	}

	return &values[0], nil
}

func (p *fakeCMProvider) GetMetricBySelector(ctx context.Context, namespace string, selector labels.Selector, info provider.CustomMetricInfo, metricSelector labels.Selector) (*custom_metrics.MetricValueList, error) {
	metricId, values, ok := p.valuesFor(types.NamespacedName{Namespace: namespace, Name: "*"}, info, metricSelector)
	if !ok {
		return nil, fmt.Errorf("non-existent metric requested (id: %s)", metricId)
	}

	var trimmedValues custom_metrics.MetricValueList

	var subsetCounts map[string]int
	if info.Namespaced {
		subsetCounts = p.namespacedSubsetCounts
	} else {
		subsetCounts = p.rootSubsetCounts
	}

	if trimmedCount, ok := subsetCounts[metricId]; ok {
		trimmedValues = custom_metrics.MetricValueList{
			Items: make([]custom_metrics.MetricValue, 0, trimmedCount),
		}
		for i := range values {
			var lbls labels.Labels
			if i < trimmedCount {
				lbls = matchingSet
			} else {
				lbls = emptySet
			}
			if selector.Matches(lbls) {
				trimmedValues.Items = append(trimmedValues.Items, custom_metrics.MetricValue{})
			}
		}
	} else {
		trimmedValues = custom_metrics.MetricValueList{
			Items: values,
		}
	}

	return &trimmedValues, nil
}

func (p *fakeCMProvider) ListAllMetrics() []provider.CustomMetricInfo {
	return p.metrics
}

type T struct {
	Method        string
	Path          string
	Status        int
	ExpectedCount int
}

func TestCustomMetricsAPI(t *testing.T) {
	totalNodesCount := 4
	totalPodsCount := 16
	matchingNodesCount := 2
	matchingPodsCount := 8

	cases := map[string]T{
		// checks which should fail
		"GET long prefix": {"GET", "/" + prefix + "/", http.StatusNotFound, 0},

		"root GET missing storage": {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/blah", http.StatusNotFound, 0},

		"GET at root resource leaf":        {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/nodes/foo", http.StatusNotFound, 0},
		"GET at namespaced resource leaft": {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/namespaces/ns/pods/bar", http.StatusNotFound, 0},

		// Positive checks to make sure everything is wired correctly
		"GET for all nodes (root)":                 {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/nodes/*/some-metric", http.StatusOK, totalNodesCount},
		"GET for all pods (namespaced)":            {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/namespaces/ns/pods/*/some-metric", http.StatusOK, totalPodsCount},
		"GET for namespace":                        {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/namespaces/ns/metrics/some-metric", http.StatusOK, 1},
		"GET for label selected nodes (root)":      {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/nodes/*/some-metric?labelSelector=foo%3Dbar", http.StatusOK, matchingNodesCount},
		"GET for label selected pods (namespaced)": {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/namespaces/ns/pods/*/some-metric?labelSelector=foo%3Dbar", http.StatusOK, matchingPodsCount},
		"GET for single node (root)":               {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/nodes/foo/some-metric", http.StatusOK, 1},
		"GET for single pod (namespaced)":          {"GET", "/" + prefix + "/" + customMetricsGroupVersion.Group + "/" + customMetricsGroupVersion.Version + "/namespaces/ns/pods/foo/some-metric", http.StatusOK, 1},
	}

	prov := &fakeCMProvider{
		rootValues: map[string][]custom_metrics.MetricValue{
			"nodes/*/some-metric":       make([]custom_metrics.MetricValue, totalNodesCount),
			"nodes/foo/some-metric":     make([]custom_metrics.MetricValue, 1),
			"namespaces/ns/some-metric": make([]custom_metrics.MetricValue, 1),
		},
		namespacedValues: map[string][]custom_metrics.MetricValue{
			"ns/pods/*/some-metric":   make([]custom_metrics.MetricValue, totalPodsCount),
			"ns/pods/foo/some-metric": make([]custom_metrics.MetricValue, 1),
		},

		rootSubsetCounts: map[string]int{
			"nodes/*/some-metric": matchingNodesCount,
		},
		namespacedSubsetCounts: map[string]int{
			"ns/pods/*/some-metric": matchingPodsCount,
		},
	}

	server := httptest.NewServer(handleCustomMetrics(prov))
	defer server.Close()
	client := http.Client{}
	for k, v := range cases {
		response, err := executeRequest(t, k, v, server, &client)
		if err != nil {
			t.Errorf(err.Error())
			continue
		}
		if v.ExpectedCount > 0 {
			lst := &cmv1beta1.MetricValueList{}
			if err := extractBody(response, lst); err != nil {
				t.Errorf("unexpected error (%s): %v", k, err)
				continue
			}
			if len(lst.Items) != v.ExpectedCount {
				t.Errorf("Expected %d items, got %d (%s): %#v", v.ExpectedCount, len(lst.Items), k, lst.Items)
				continue
			}
		}
	}
}

func TestExternalMetricsAPI(t *testing.T) {
	cases := map[string]T{
		// checks which should fail

		"GET long prefix":             {"GET", "/" + prefix + "/", http.StatusNotFound, 0},
		"GET at root scope":           {"GET", "/" + prefix + "/" + externalMetricsGroupVersion.Group + "/" + externalMetricsGroupVersion.Version + "/nonexistent-metric", http.StatusNotFound, 0},
		"GET without metric name":     {"GET", "/" + prefix + "/" + externalMetricsGroupVersion.Group + "/" + externalMetricsGroupVersion.Version + "/namespaces/foo", http.StatusNotFound, 0},
		"GET for metric with slashes": {"GET", "/" + prefix + "/" + externalMetricsGroupVersion.Group + "/" + externalMetricsGroupVersion.Version + "/namespaces/foo/group/metric", http.StatusNotFound, 0},

		// Positive checks to make sure everything is wired correctly
		"GET for external metric":               {"GET", "/" + prefix + "/" + externalMetricsGroupVersion.Group + "/" + externalMetricsGroupVersion.Version + "/namespaces/default/my-external-metric", http.StatusOK, 2},
		"GET for external metric with selector": {"GET", "/" + prefix + "/" + externalMetricsGroupVersion.Group + "/" + externalMetricsGroupVersion.Version + "/namespaces/default/my-external-metric?labelSelector=foo%3Dbar", http.StatusOK, 1},
		"GET for nonexistent metric":            {"GET", "/" + prefix + "/" + externalMetricsGroupVersion.Group + "/" + externalMetricsGroupVersion.Version + "/namespaces/foo/nonexistent-metric", http.StatusOK, 0},
	}

	// "real" fake provider implementation can be used in test, because it doesn't have any dependencies.
	// Note: this provider has a hardcoded list of external metrics.
	prov, _ := sampleprovider.NewFakeProvider(nil, nil)

	server := httptest.NewServer(handleExternalMetrics(prov))
	defer server.Close()
	client := http.Client{}
	for k, v := range cases {
		response, err := executeRequest(t, k, v, server, &client)
		if err != nil {
			t.Errorf(err.Error())
			continue
		}
		if v.ExpectedCount > 0 {
			lst := &emv1beta1.ExternalMetricValueList{}
			if err := extractBody(response, lst); err != nil {
				t.Errorf("unexpected error (%s): %v", k, err)
				continue
			}
			if len(lst.Items) != v.ExpectedCount {
				t.Errorf("Expected %d items, got %d (%s): %#v", v.ExpectedCount, len(lst.Items), k, lst.Items)
				continue
			}
		}
	}
}

func executeRequest(t *testing.T, k string, v T, server *httptest.Server, client *http.Client) (*http.Response, error) {
	request, err := http.NewRequest(v.Method, server.URL+v.Path, nil)
	if err != nil {
		t.Fatalf("unexpected error (%s): %v", k, err)
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("unexpected error (%s): %v", k, err)
	}

	if response.StatusCode != v.Status {
		body, err := extractBodyString(response)
		bodyPart := body
		if err != nil {
			bodyPart = fmt.Sprintf("[error extracting body: %v]", err)
		}
		return nil, fmt.Errorf("Expected %d for %s (%s), Got %#v -- %s", v.Status, v.Method, k, response, bodyPart)
	}
	return response, nil
}
