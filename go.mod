module sigs.k8s.io/custom-metrics-apiserver

go 1.16

require (
	github.com/emicklei/go-restful v2.15.0+incompatible
	github.com/emicklei/go-restful-swagger12 v0.0.0-20201014110547-68ccff494617
	github.com/go-openapi/spec v0.20.3
	github.com/googleapis/gnostic v0.4.1
	github.com/spf13/pflag v1.0.5
	github.com/stretchr/testify v1.7.0
	k8s.io/api v0.21.2
	k8s.io/apimachinery v0.21.2
	k8s.io/apiserver v0.21.2
	k8s.io/client-go v0.21.2
	k8s.io/component-base v0.21.2
	k8s.io/klog/v2 v2.8.0
	k8s.io/kube-openapi v0.0.0-20210305001622-591a79e4bda7
	k8s.io/metrics v0.21.2
	k8s.io/utils v0.0.0-20210707171843-4b05e18ac7d9
)
