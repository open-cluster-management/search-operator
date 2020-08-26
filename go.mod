module github.com/open-cluster-management/search-operator

go 1.13

require (
	github.com/operator-framework/operator-sdk v0.17.0
	github.com/spf13/pflag v1.0.5
	k8s.io/api v0.17.4
	k8s.io/apimachinery v0.17.4
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.5.2
)

// Pinned to kubernetes-1.16.2
replace (
	github.com/Azure/go-autorest => github.com/Azure/go-autorest v13.3.2+incompatible // Required by OLM
	github.com/coreos/etcd => github.com/coreos/etcd v3.3.24+incompatible
	github.com/hashicorp/consul => github.com/hashicorp/consul v1.7.4
	github.com/openshift/origin => github.com/openshift/origin v1.2.0
	github.com/prometheus/prometheus => github.com/prometheus/prometheus v2.7.1+incompatible
	k8s.io/client-go => k8s.io/client-go v0.17.4 // Required by prometheus-operator
)

replace github.com/docker/docker => github.com/moby/moby v0.7.3-0.20190826074503-38ab9da00309 // Required by Helm
