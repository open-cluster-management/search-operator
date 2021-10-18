# search-operator

Operator for the Search Service.
This Operator will create the `redisgraph-user-secret` and `search-redisgraph` statefulset. The `search-redisgraph` statefulset uses the `searchoperator` CR instance created during install process for the initial redisgraph pod configuration. The user has an option to update the pod configuration using `searchcustomization` CR.  The goal is to move the search-chart in the near future.

## Development

This project was created with the [operator-sdk](https://v1-2-x.sdk.operatorframework.io/docs/).  About 90% of the code is automated boilerplate generated by the operator-sdk.
To learn more about how to update this project check the [getting-started guide](https://v1-2-x.sdk.operatorframework.io/docs/)

Most of the code in this project is auto-generated.  The logic specific to this operator is at ./pkg/controllers/searchoperator_controller.go

### Install the Operator SDK CLI

Follow the steps in the [installation guide][install_guide] to learn how to install the Operator SDK CLI tool. It requires [version v1.2][operator_sdk_v1.2].
Download and install `operator-sdk` for Mac:

```bash
https://v1-2-x.sdk.operatorframework.io/docs/installation/install-operator-sdk/
```

### Build the Operator

- git clone this repository.
- operator-sdk init --domain="" --repo=github.com/open-cluster-management/search-operator   --verbose
- make generate
- make manifests
- make docker-build

Update the search-operator deployment yaml with this generated docker image to test your changes. You may have to use personal quay.io repository or build the docker image in travis.

To create new APIs follow the example command for SearchCustomization CRD.

- operator-sdk create api --group search.open-cluster-management.io  --version v1alpha1 --kind SearchCustomization --resource=true --controller=true

Rebuild: 2021-10-18
