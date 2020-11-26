#!/bin/bash
set -e

echo "> Running build/run-e2e-tests.sh"
# export DOCKER_IMAGE_AND_TAG=${1}

# make e2e-tests

echo "Running e2e test"

echo "<repo>/<component>:<tag> : $1"

IMAGE_NAME=$1
echo "IMAGE: " $IMAGE_NAME

DEFAULT_NS="open-cluster-management"
HUB_KUBECONFIG=$HOME/.kube/kind-config-hub
WORKDIR=`pwd`

sed_command='sed -i-e -e'
if [[ "$(uname)" == "Darwin" ]]; then
	sed_command='sed -i '-e' -e'
fi

deploy() {
    setup_kubectl_and_oc_command
	create_kind_hub
	deploy_search_operator
	# deploy_metrics_collector $IMAGE_NAME
	# delete_kind_hub	
}

setup_kubectl_and_oc_command() {
	echo "=====Setup kubectl and oc=====" 
	# kubectl required for kind
	# oc client required for installing operators
	# if and when we are feeling ambitious... also download the installer and install ocp, and run our component integration test here	
	# uname -a and grep mac or something...
    # Darwin MacBook-Pro 19.5.0 Darwin Kernel Version 19.5.0: Tue May 26 20:41:44 PDT 2020; root:xnu-6153.121.2~2/RELEASE_X86_64 x86_64
	echo "Install kubectl and oc from openshift mirror (https://mirror.openshift.com/pub/openshift-v4/clients/ocp/4.4.14/openshift-client-mac-4.4.14.tar.gz)" 
	mv README.md README.md.tmp 
    if [[ "$(uname)" == "Darwin" ]]; then # then we are on a Mac 
		curl -LO https://mirror.openshift.com/pub/openshift-v4/clients/ocp/4.4.14/openshift-client-mac-4.4.14.tar.gz 
		tar xzvf openshift-client-mac-4.4.14.tar.gz  # xzf to quiet logs
		rm openshift-client-mac-4.4.14.tar.gz
    elif [[ "$(uname)" == "Linux" ]]; then # we are in travis, building in rhel 
		curl -LO https://mirror.openshift.com/pub/openshift-v4/clients/ocp/4.4.14/openshift-client-linux-4.4.14.tar.gz
		tar xzvf openshift-client-linux-4.4.14.tar.gz  # xzf to quiet logs
		rm openshift-client-linux-4.4.14.tar.gz
    fi
	# this package has a binary, so:

	echo "Current directory"
	echo $(pwd)
	# mv README.md.tmp README.md 
	chmod +x ./kubectl
	if [[ ! -f /usr/local/bin/kubectl ]]; then
		sudo cp ./kubectl /usr/local/bin/kubectl
	fi
	chmod +x ./oc
	if [[ ! -f /usr/local/bin/oc ]]; then
		sudo cp ./oc /usr/local/bin/oc
	fi
	# kubectl and oc are now installed in current dir 
	echo -n "kubectl version" && kubectl version
 	# echo -n "oc version" && oc version 
}

create_kind_hub() { 
    WORKDIR=`pwd`
    if [[ ! -f /usr/local/bin/kind ]]; then
    
    	echo "=====Create kind cluster=====" 
    	echo "Install kind from (https://kind.sigs.k8s.io/)."
    
    	# uname returns your operating system name
    	# uname -- Print operating system name
    	# -L location, lowercase -o specify output name, uppercase -O Write  output to a local file named like the remote file we get  
    	curl -Lo ./kind "https://kind.sigs.k8s.io/dl/v0.7.0/kind-$(uname)-amd64"
    	chmod +x ./kind
    	sudo cp ./kind /usr/local/bin/kind
    fi
    echo "Delete hub if it exists"
    kind delete cluster --name hub || true
    
    echo "Start hub cluster" 
    rm -rf $HOME/.kube/kind-config-hub
    kind create cluster --kubeconfig $HOME/.kube/kind-config-hub --name hub --config ${WORKDIR}/test/kind-hub-config.yaml
    kubectl cluster-info --context kind-hub --kubeconfig $(pwd)/.kube/kind-config-hub # confirm connection 
    export KUBECONFIG=$HOME/.kube/kind-config-hub
} 

deploy_search_operator() {
	echo "=====Deploying search-operator====="
	echo -n "Switch to namespace: " && kubectl config set-context --current --namespace open-cluster-management

	echo "Current directory"
	echo $(pwd)
	# git clone https://github.com/open-cluster-management/search-operator.git

	# cd search-operator
    echo -n "Applying search operator CRD:" && kubectl apply -f ./config/crd/bases/search.open-cluster-management.io_searchoperators.yaml
	echo -n "Create namespace open-cluster-management: " && kubectl create namespace open-cluster-management
    echo -n "Applying sample search operator: "  && kubectl apply -f ./config/samples/search.open-cluster-management.io_v1_searchoperator.yaml
	echo -n "Applying search operator service account: "  && kubectl apply -f ./deploy/service_account.yaml

	echo -n "Creating pull secret: " && kubectl create secret docker-registry multiclusterhub-operator-pull-secret --docker-server=quay.io --docker-username=$DOCKER_USER --docker-password=$DOCKER_PASS 
  	echo -n "\nApplying search operator deployment: "  && kubectl apply -f ./test/operator-deployment.yaml
	# apply yamls 

}
deploy