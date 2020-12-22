#!/bin/bash
# Copyright (c) 2020 Red Hat, Inc.

echo $1

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
	test_default_pvc
	test_no_persistence
	test_degraded_mode
	#delete_kind_hub	
	#delete_command_binaries	
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
	mv README.md.tmp README.md 
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
    # kubectl cluster-info --context kind-hub --kubeconfig $(pwd)/.kube/kind-config-hub # confirm connection 
    export KUBECONFIG=$HOME/.kube/kind-config-hub
	echo "KUBECONFIG" && echo $KUBECONFIG
} 


delete_kind_hub() {
	echo "====Delete kind cluster====="
    kind delete cluster --name hub
}

delete_command_binaries(){
	cd ${WORKDIR}/..
	echo "Current directory"
	echo $(pwd)
	rm ./kind
	rm ./kubectl
	rm ./oc
}
test_default_pvc() {
	echo "=====Deploying search-operator====="
	echo -n "Switch to namespace: " && kubectl config set-context --current --namespace open-cluster-management

	echo "Current directory"
	echo $(pwd)
	echo -n "Create namespace open-cluster-management-monitoring: " && kubectl create namespace open-cluster-management
    echo -n "Creating pull secret: " && kubectl create secret docker-registry search-operator-pull-secret --docker-server=quay.io --docker-username=$DOCKER_USER --docker-password=$DOCKER_PASS
	# git clone https://github.com/open-cluster-management/search-operator.git

	# cd search-operator
    echo -n "Applying search operator CRD:" && kubectl apply -f ./config/crd/bases/search.open-cluster-management.io_searchoperators.yaml

    echo -n "Applying sample search operator: "  && kubectl apply -f ./config/samples/search.open-cluster-management.io_v1_searchoperator.yaml
	echo -n "Applying search operator service account: "  && kubectl apply -f ./test/service_account.yaml
	echo -n "Applying search operator role: "  && kubectl apply -f ./deploy/role.yaml
	echo -n "Applying search operator role binding: "  && kubectl apply -f ./deploy/role_binding.yaml
    echo -n "Applying redisgraph user secret : "  && kubectl apply -f ./test/redisgraph-user-secret.yaml
	echo -n "Applying redisgraph tls secret : "  && kubectl apply -f ./test/redisgraph-tls-secret.yaml
	echo -n "Creating pull secret for statefulset : " && kubectl create secret docker-registry multiclusterhub-operator-pull-secret --docker-server=quay.io --docker-username=$DOCKER_USER --docker-password=$DOCKER_PASS 

	echo -n "Applying search operator deployment: "  && kubectl apply -f ./test/operator-deployment.yaml
	echo "Waiting 2 minutes for the redisgraph pod to get Ready... " && sleep 120
	count=0
	while true ; do
	  SEARCHOPERATOR=$(kubectl get searchoperator searchoperator -n open-cluster-management -o json | jq '.status.persistence')
	  echo $SEARCHOPERATOR
	  count=`expr $count + 1`
	  if [[ "$SEARCHOPERATOR" == "\"Redisgraph is using PersistenceVolumeClaim\"" ]]
	  then
	     echo "SUCCESS - Redisgraph Pod Ready"
		 break
	  fi
	  echo "No Sucess yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod"
		 exit 1
	  fi
	done
}
test_no_persistence() {
	echo "=====Update  search-operator to persistence false====="
	echo -n "Patch searchoperator: " && kubectl patch searchoperator searchoperator -p '{"spec":{"persistence":false}}'  --type='merge'
	echo -n "Delete PVC : " && kubectl delete pvc redisgraph-pvc
	echo "Waiting 2 minutes for the redisgraph pod to get Ready... " && sleep 120
    count=0
	while true ; do
	  SEARCHOPERATOR=$(kubectl get searchoperator searchoperator -n open-cluster-management -o json | jq '.status.persistence')
	  echo $SEARCHOPERATOR
	  count=`expr $count + 1`
	  if [[ "$SEARCHOPERATOR" == "\"Node level persistence using EmptyDir\"" ]]
	  then
	     echo "SUCCESS - Redisgraph Pod Ready"
		 break
	  fi
	  echo "No Sucess yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod"
		 exit 1
	  fi
	done
}
test_degraded_mode() {
	echo "=====Update  search-operator to persistence true and invalid storage class====="
	echo -n "Patch searchoperator: " && kubectl patch searchoperator searchoperator -p '{"spec":{"persistence":true,"storageclass": "test"}}'  --type='merge'
	echo "Waiting 4 minutes for the redisgraph pod to get Ready... " && sleep 240
	count=0
	while true ; do
	  SEARCHOPERATOR=$(kubectl get searchoperator searchoperator -n open-cluster-management -o json | jq '.status.persistence')
	  echo $SEARCHOPERATOR
	  count=`expr $count + 1`
	  if [[ "$SEARCHOPERATOR" == "\"Degraded mode using EmptyDir. Unable to use PersistenceVolumeClaim\"" ]]
	  then
	     echo "SUCCESS - Redisgraph Pod Ready"
		 exit 0
	  fi
	  echo "No Sucess yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod"
		 exit 1
	  fi
	done 
}
deploy