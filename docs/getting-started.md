# Getting started with developing your own Custom Metrics API Server

_Note: This tutorial is undergoing changes in this library's transition to using Go modules. Use at your own risk while fixes are underway!_

This will walk through writing a very basic custom metrics API server using
this library. The implementation will be static.  With a real adapter, you'd
generally be reading from some external metrics system instead.

The end result will look similar to the [test adapter](/test-adapter), but
will generate sample metrics automatically instead of setting them via an
HTTP endpoint.

## Prerequisites

Create a project and initialize the dependencies like so:

```shell
$ mkdir $GOPATH/src/github.com/$USER/$REPO
$ cd $GOPATH/src/github.com/$USER/$REPO
$ go mod init
```

## Writing the Code

There's two parts to an adapter: the setup code, and the providers.  The
setup code initializes the API server, and the providers handle requests
from the API for metrics.

### Writing a provider

There are currently two provider interfaces, corresponding to two
different APIs: the custom metrics API (for metrics that describe
Kubernetes objects), and the external metrics API (for metrics that don't
describe kubernetes objects, or are otherwise not attached to a particular
object). For the sake of brevity, this walkthrough will show an example of
the custom metrics API, but a full example including the external metrics
API can be found in the [test adapter](/test-adapter).

Put your provider in the `pkg/provider` directory in your repository.

<details>

<summary>To get started, you'll need some imports:</summary>

```go
package provider

import (
    "fmt"
    "time"

    "k8s.io/klog/v2"
    apierr "k8s.io/apimachinery/pkg/api/errors"
    apimeta "k8s.io/apimachinery/pkg/api/meta"
    "k8s.io/apimachinery/pkg/api/resource"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/labels"
    "k8s.io/apimachinery/pkg/runtime"
    "k8s.io/apimachinery/pkg/runtime/schema"
    "k8s.io/client-go/dynamic"
    "k8s.io/metrics/pkg/apis/custom_metrics"

    "sigs.k8s.io/custom-metrics-apiserver/pkg/provider"
    "k8s.io/metrics/pkg/apis/external_metrics"
)
```

</details>

The custom metrics provider interface, which you'll need to implement, is
called `CustomMetricsProvider`, and looks like this:

```go
type CustomMetricsProvider interface {
    ListAllMetrics() []CustomMetricInfo

    GetMetricByName(name types.NamespacedName, info CustomMetricInfo) (*custom_metrics.MetricValue, error)
    GetMetricBySelector(namespace string, selector labels.Selector, info CustomMetricInfo) (*custom_metrics.MetricValueList, error)
}
```

First, there's a method for listing all metrics available at any point in
time.  It's used to populate the discovery information in the API, so that
clients can know what metrics are available.  It's not allowed to fail (it
doesn't return any error), and it should return quickly, so it's suggested
that you update it asynchronously in real-world code.

For this walkthrough, you can just return a few statically-named metrics,
two that are namespaced, and one that's on namespaces themselves, and thus
root-scoped:

```go
func (p *yourProvider) ListAllMetrics() []provider.CustomMetricInfo {
    return []provider.CustomMetricInfo{
        // these are mostly arbitrary examples
        {
            GroupResource: schema.GroupResource{Group: "", Resource: "pods"},
            Metric:        "packets-per-second",
            Namespaced:    true,
        },
        {
            GroupResource: schema.GroupResource{Group: "", Resource: "services"},
            Metric:        "connections-per-second",
            Namespaced:    true,
        },
        {
            GroupResource: schema.GroupResource{Group: "", Resource: "namespaces"},
            Metric:        "work-queue-length",
            Namespaced:    false,
        },
    }
}
```

Next, you'll need to implement the methods that actually fetch the
metrics. There are methods for fetching metrics describing arbitrary Kubernetes
resources, both root-scoped and namespaced-scoped.  Those metrics can
either be fetched for a single object, or for a list of objects by
selector.

<details>

<summary>Make sure you understand resources, scopes, and quantities before
continuing.</summary>

---

#### Kinds, Resources, and Scopes

When working with these APIs (and Kubernetes APIs in general), you'll
often see references to resources, kinds, and scope.

Kinds refer to types within the API.  For instance, `Deployment` is
a kind.  When you fully-qualify a kind, you write it as
a *group-version-kind*, or *GVK* for short.  The GVK for `Deployment` is
`apps/v1.Deployment` (or `{Group: "apps", Version: "v1", Kind:
"Deployment"}` in Go syntax).

Resources refer to URLs in an API, in an abstract sense.  Each resource
has a corresponding kind, but that kind isn't necessarily unique to that
particular resource.  For instance, the resource `deployments` has kind
`Deployment`, but so does the subresource `deployments/status`.  On the
other hand, all the `*/scale` subresources use the kind `Scale` (for the
most part).  When you fully-qualify a resource, you write it as
a *group-version-resource*, or GVR for short.  The GVR `{Group: "apps",
Version: "v1", Resource: "deployments"}` corresponds to the URL form
`/apis/apps/v1/namespaces/<ns>/deployments`.  Resources may be singular or
plural -- both effectively refer to the same thing.

Sometimes you might partially qualify a particular kind or resource as
a *group-kind* or *group-resource*, leaving off the versions.  You write
group-resources as `<resource>.<group>`, like `deployments.apps` in the
custom metrics API (and in kubectl).

Scope refers to whether or not a particular resource is grouped under
namespaces.  You say that namespaced resources, like `deployments` are
*namespace-scoped*, while non-namespaced resources, like `nodes` are
*root-scoped*.

To figure out which kinds correspond to which resources, and which
resources have what scope, you use a `RESTMapper`.  The `RESTMapper`
generally collects its information from *discovery* information, which
lists which kinds and resources are available in a particular Kubernetes
cluster.

#### Quantities

When dealing with metrics, you'll often need to deal with fractional
numbers.  While many systems use floating point numbers for that purpose,
Kubernetes instead uses a system called *quantities*.

Quantities are whole numbers suffixed with SI suffixes.  You use the `m`
suffix (for milli-units) to denote numbers with fractional components,
down the thousandths place.

For instance, `10500m` means `10.5` in decimal notation. To construct
a new quantity out of a milli-unit value (e.g. millicores or millimeters),
you'd use the `resource.NewMilliQuantity(valueInMilliUnits,
resource.DecimalSI)` function.  To construct a new quantity that's a whole
number, you can either use `NewMilliQuantity` and multiple by `1000`, or
use the `resource.NewQuantity(valueInWholeUnits, resource.DecimalSI)`
function.

Remember that in both cases, the argument *must* be an integer, so if you
need to represent a number with a fractional component, use
`NewMilliQuantity`.

---

</details>

You'll need a handle to a RESTMapper (to map between resources and kinds)
and dynamic client to fetch lists of objects in the cluster, if you don't
already have sufficient information in your metrics pipeline:

```go
type yourProvider struct {
    client dynamic.Interface
    mapper apimeta.RESTMapper

    // just increment values when they're requested
    values map[provider.CustomMetricInfo]int64
}
```

Then, you can implement the methods that fetch the metrics.  In this
walkthrough, those methods will just increment values for metrics as
they're fetched.  In real adapter, you'd want to fetch metrics from your
backend in these methods.

First, a couple of helpers, which support doing the fake "fetch"
operation, and constructing a result object:

```go
// valueFor fetches a value from the fake list and increments it.
func (p *yourProvider) valueFor(info provider.CustomMetricInfo) (int64, error) {
    // normalize the value so that you treat plural resources and singular
    // resources the same (e.g. pods vs pod)
    info, _, err := info.Normalized(p.mapper)
    if err != nil {
        return 0, err
    }

    value := p.values[info]
    value += 1
    p.values[info] = value

    return value, nil
}

// metricFor constructs a result for a single metric value.
func (p *testingProvider) metricFor(value int64, name types.NamespacedName, info provider.CustomMetricInfo) (*custom_metrics.MetricValue, error) {
    // construct a reference referring to the described object
    objRef, err := helpers.ReferenceFor(p.mapper, name, info)
    if err != nil {
        return nil, err
    }

    return &custom_metrics.MetricValue{
        DescribedObject: objRef,
        MetricName:      info.Metric,
        // you'll want to use the actual timestamp in a real adapter
        Timestamp:       metav1.Time{time.Now()},
        Value:           *resource.NewMilliQuantity(value*100, resource.DecimalSI),
    }, nil
}
```

Then, you'll need to implement the two main methods.  The first fetches
a single metric value for one object (for example, for the `object` metric
type in the HorizontalPodAutoscaler):

```go
func (p *yourProvider) GetMetricByName(name types.NamespacedName, info provider.CustomMetricInfo) (*custom_metrics.MetricValue, error) {
    value, err := p.valueFor(info)
    if err != nil {
        return nil, err
    }
    return p.metricFor(value, name, info)
}
```

The second fetches multiple metric values, one for each object in a set
(for example, for the `pods` metric type in the HorizontalPodAutoscaler).

```go
func (p *yourProvider) GetMetricBySelector(namespace string, selector labels.Selector, info provider.CustomMetricInfo) (*custom_metrics.MetricValueList, error) {
    totalValue, err := p.valueFor(info)
    if err != nil {
        return nil, err
    }

    names, err := helpers.ListObjectNames(p.mapper, p.client, namespace, selector, info)
    if err != nil {
        return nil, err
    }

    res := make([]custom_metrics.MetricValue, len(names))
    for i, name := range names {
        // in a real adapter, you might want to consider pre-computing the
        // object reference created in metricFor, instead of recomputing it
        // for each object.
        value, err := p.metricFor(100*totalValue/int64(len(res)), types.NamespacedName{Namespace: namespace, Name: name}, info)
        if err != nil {
            return nil, err
        }
        res[i] = *value
    }

    return &custom_metrics.MetricValueList{
        Items: res,
    }, nil
}
```

Now, you just need to plug in your provider to an API server.

### Writing the setup code

The adapter library provides helpers to construct an API server to serve
the metrics provided by your provider.

<details>

<summary>First, you'll need a few imports:</summary>

```go
package main

import (
    "flag"
    "os"

    "github.com/golang/klog"
    "k8s.io/apimachinery/pkg/util/wait"
    "k8s.io/component-base/logs"

    basecmd "sigs.k8s.io/custom-metrics-apiserver/pkg/cmd"
    "sigs.k8s.io/custom-metrics-apiserver/pkg/provider"

    // make this the path to the provider that you just wrote
    yourprov "github.com/user/repo/pkg/provider"
)
```

</details>

With those out of the way, you can make use of the `basecmd.AdapterBase`
struct to help set up the API server:

```go
type YourAdapter struct {
    basecmd.AdapterBase

    // the message printed on startup
    Message string
}

func main() {
    logs.InitLogs()
    defer logs.FlushLogs()

    // initialize the flags, with one custom flag for the message
    cmd := &YourAdapter{}
    cmd.Flags().StringVar(&cmd.Message, "msg", "starting adapter...", "startup message")
    cmd.Flags().AddGoFlagSet(flag.CommandLine) // make sure you get the klog flags
    cmd.Flags().Parse(os.Args)

    provider := cmd.makeProviderOrDie()
    cmd.WithCustomMetrics(provider)
    // you could also set up external metrics support,
    // if your provider supported it:
    // cmd.WithExternalMetrics(provider)

    klog.Infof(cmd.Message)
    if err := cmd.Run(wait.NeverStop); err != nil {
        klog.Fatalf("unable to run custom metrics adapter: %v", err)
    }
}
```

Finally, you'll need to add a bit of setup code for the specifics of your
provider.  This code will be specific to the options of your provider --
you might need to pass configuration for connecting to the backing metrics
solution, extra credentials, or advanced configuration.  For the provider
you wrote above, the setup code looks something like this:

```go
func (a *SampleAdapter) makeProviderOrDie() provider.CustomMetricsProvider {
    client, err := a.DynamicClient()
    if err != nil {
        klog.Fatalf("unable to construct dynamic client: %v", err)
    }

    mapper, err := a.RESTMapper()
    if err != nil {
        klog.Fatalf("unable to construct discovery REST mapper: %v", err)
    }

    return yourprov.NewProvider(client, mapper)
}
```

## Build the project

Now that you have a working adapter, you can build it with `go build`, and
stick in it a container, and deploy it onto the cluster.  Check out the
[test adapter deployment files](/test-adapter-deploy) for an example of
how to do that.
