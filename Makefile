REGISTRY?=kubernetes-sigs
IMAGE?=k8s-test-metrics-adapter
TEMP_DIR:=$(shell mktemp -d)
ARCH?=amd64
OUT_DIR?=./_output

VERSION?=latest

.PHONY: all
all: build-test-adapter

.PHONY: build-test-adapter
build-test-adapter: test-adapter/generated/openapi/zz_generated.openapi.go
	CGO_ENABLED=0 GOOS=linux GOARCH=$(ARCH) go build -o $(OUT_DIR)/$(ARCH)/test-adapter sigs.k8s.io/custom-metrics-apiserver/test-adapter

test-adapter/generated/openapi/zz_generated.openapi.go: go.mod go.sum
	go install -mod=readonly k8s.io/kube-openapi/cmd/openapi-gen
	$(GOPATH)/bin/openapi-gen --logtostderr \
	    -i k8s.io/metrics/pkg/apis/custom_metrics,k8s.io/metrics/pkg/apis/custom_metrics/v1beta1,k8s.io/metrics/pkg/apis/custom_metrics/v1beta2,k8s.io/metrics/pkg/apis/external_metrics,k8s.io/metrics/pkg/apis/external_metrics/v1beta1,k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/api/resource,k8s.io/apimachinery/pkg/version,k8s.io/api/core/v1 \
	    -h ./hack/boilerplate.go.txt \
	    -p ./test-adapter/generated/openapi \
	    -O zz_generated.openapi \
	    -o ./ \
	    -r /dev/null

.PHONY: gofmt
gofmt:
	./hack/gofmt-all.sh

.PHONY: verify
verify: verify-deps verify-gofmt

.PHONY: verify-deps
verify-deps:
	go mod verify
	go mod tidy
	@git diff --exit-code -- go.sum go.mod

.PHONY: verify-gofmt
verify-gofmt:
	./hack/gofmt-all.sh -v

.PHONY: test
test:
	CGO_ENABLED=0 go test ./pkg/...

.PHONY: test-adapter-container
test-adapter-container: build-test-adapter
	cp test-adapter-deploy/Dockerfile $(TEMP_DIR)
	cp $(OUT_DIR)/$(ARCH)/test-adapter $(TEMP_DIR)/adapter
	cd $(TEMP_DIR) && sed -i.bak "s|BASEIMAGE|scratch|g" Dockerfile
	sed -i.bak 's|REGISTRY|'${REGISTRY}'|g' test-adapter-deploy/testing-adapter.yaml
	docker build -t $(REGISTRY)/$(IMAGE)-$(ARCH):$(VERSION) $(TEMP_DIR)
	rm -rf $(TEMP_DIR) test-adapter-deploy/testing-adapter.yaml.bak

.PHONY: test-kind
test-kind:
	kind load docker-image $(REGISTRY)/$(IMAGE)-$(ARCH):$(VERSION)
	kubectl apply -f test-adapter-deploy/testing-adapter.yaml
	kubectl rollout restart -n custom-metrics deployment/custom-metrics-apiserver
