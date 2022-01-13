#!/bin/bash
# Copyright (c) 2020 Red Hat, Inc.
# Copyright Contributors to the Open Cluster Management project

echo $1

IMAGE_NAME=$1
echo "IMAGE: " $IMAGE_NAME

DEFAULT_NS="open-cluster-management"
HUB_KUBECONFIG=$HOME/.kube/kind-config-hub
WORKDIR=/tmp/search-operator

sed_command='sed -i-e -e'
if [[ "$(uname)" == "Darwin" ]]; then
	sed_command='sed -i '-e' -e'
fi

if [[ -z "${DOCKER_USER}" ]]; then
  DOCKER_USER="stolostron+acmsearch"
fi

if [[ -z "${DOCKER_PASS}" ]]; then
  DOCKER_PASS=$2
fi


deploy() {
    setup_kubectl_and_oc_command
	create_kind_hub
	initial_setup
	test_default_pvc
	apply_customizationCR
	test_no_persistence
	test_pvc_creation_with_custom_storage_settings
	test_invalid_storageclass
	test_update_podresource_search_operator
	test_fallback_emptydir
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
   
    #installing jq
    if [[ "$(uname)" == "Linux" ]]; then
		echo "Install jq on Linux"
            curl -o jq -L https://github.com/stedolan/jq/releases/download/jq-1.6/jq-linux64
        elif [[ "$(uname)" == "Darwin" ]]; then
		echo "Install jq on Darwin"
          curl -o jq -L https://github.com/stedolan/jq/releases/download/jq-1.6/jq-osx-amd64
    fi
    chmod +x ./jq 
    sudo cp ./jq /usr/bin/jq
	echo $PATH
	export PATH=$PATH:.
	echo $PATH
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
    WORKDIR=/tmp/search-operator
    if [[ ! -f /usr/local/bin/kind ]]; then
    
    	echo "=====Create kind cluster=====" 
    	echo "Install kind from (https://kind.sigs.k8s.io/)."
    
    	# uname returns your operating system name
    	# uname -- Print operating system name
    	# -L location, lowercase -o specify output name, uppercase -O. Write output to a local file named like the remote file we get  
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

initial_setup() {
echo "=====Initial setup for tests====="
	echo -n "Switch to namespace: " && kubectl config set-context --current --namespace open-cluster-management
        cd ${WORKDIR}
	echo "Current directory"
	echo $(pwd)
	echo -n "Create namespace open-cluster-management: " && kubectl create namespace open-cluster-management
    echo -n "Creating pull secret: " && kubectl create secret docker-registry search-operator-pull-secret --docker-server=quay.io --docker-username=$DOCKER_USER --docker-password=$DOCKER_PASS
	# git clone https://github.com/stolostron/search-operator.git

	# cd search-operator
    echo -n "Applying search operator CRD:" && kubectl apply -f ./config/crd/bases/search.open-cluster-management.io_searchoperators.yaml
    echo -n "Applying search customization CRD:" && kubectl apply -f ./config/crd/bases/search.open-cluster-management.io_searchcustomizations.yaml
    echo -n "Applying sample search operator: " && kubectl apply -f ./config/samples/search.open-cluster-management.io_v1alpha1_searchoperator.yaml
	echo -n "Applying search operator service account: " && kubectl apply -f ./test/service_account.yaml
	echo -n "Applying search operator role: " && kubectl apply -f ./deploy/role.yaml
	echo -n "Applying search operator role binding: " && kubectl apply -f ./deploy/role_binding.yaml
    echo -n "Applying redisgraph user secret : " && kubectl apply -f ./test/redisgraph-user-secret.yaml
	echo -n "Applying redisgraph tls secret : " && kubectl apply -f ./test/redisgraph-tls-secret.yaml
	echo -n "Creating pull secret for statefulset : " && kubectl create secret docker-registry multiclusterhub-operator-pull-secret --docker-server=quay.io --docker-username=$DOCKER_USER --docker-password=$DOCKER_PASS 
    echo "=====Deploying search-operator====="
	$sed_command "s~{{ OPERATOR_IMAGE }}~$IMAGE_NAME~g" ./test/operator-deployment.yaml
	echo -n "Applying search operator deployment: " && kubectl apply -f ./test/operator-deployment.yaml
	echo -n "Set DEPLOY_REDISGRAPH variable: " && kubectl set env deploy search-operator DEPLOY_REDISGRAPH="true"
	# # change the image name back to OPERATOR_IMAGE in operator deployment for next run
	sed -i '-e' "s~$IMAGE_NAME~{{ OPERATOR_IMAGE }}~g" ./test/operator-deployment.yaml
}
test_default_pvc() {
	echo $PATH
	echo $(jq --version)
	echo "Waiting 2 minutes for the redisgraph pod to get Ready... " && sleep 120
	kubectl logs `kubectl get pods | grep search-operator |cut -d ' '  -f1`
	kubectl logs `kubectl get pods | grep redis |cut -d ' '  -f1`
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
	  echo "No Success yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod"
		 exit 1
	  fi
	done
}

apply_customizationCR() {
	echo -n "Applying sample search customization: " && kubectl apply -f ./config/samples/search.open-cluster-management.io_v1alpha1_searchcustomization.yaml
}
test_no_persistence() {
	echo "=====Disable search persistence====="
	echo -n "Patch searchcustomization: " && kubectl patch searchcustomization searchcustomization -p '{"spec":{"persistence":false}}' --type='merge'
	echo -n "Delete PVC : " && kubectl delete pvc search-redisgraph-pvc-0
	echo "Waiting 2 minutes for the redisgraph pod to get Ready... " && sleep 120
    count=0
	while true ; do
	  SEARCHOPERATOR=$(kubectl get searchoperator searchoperator -n open-cluster-management -o json | jq '.status.persistence')
	  echo $SEARCHOPERATOR
	  count=`expr $count + 1`
	  if [[ "$SEARCHOPERATOR" == "\"Redisgraph pod running with persistence disabled\"" ]]
	  then
	     echo "SUCCESS - Redisgraph Pod Ready"
		 break
	  fi
	  echo "No Success yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod"
		 exit 1
	  fi
	done
	SEARCHCUSTOMIZATIONS1=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.persistence')
	SEARCHCUSTOMIZATIONS2=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.storageClass')
	SEARCHCUSTOMIZATIONS3=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.storageSize')
	if [[ "$SEARCHCUSTOMIZATIONS1" == "false" && "$SEARCHCUSTOMIZATIONS2" == "\"\"" && "$SEARCHCUSTOMIZATIONS3" == "\"\"" ]]
	  then
	     echo "STATUS verified in SearchCustomization"
	  else
	     echo "STATUS Verification Failed in SearchCustomization"
		 exit 1	 
	fi
}

test_pvc_creation_with_custom_storage_settings() {
	echo "=====Update searchcustomization with valid storage settings====="
	echo -n "Patch searchcustomization: " && kubectl patch searchcustomization searchcustomization -p '{"spec":{"persistence":true,"storageClass": "standard", "storageSize": "2Gi"}}' --type='merge'
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
	  echo "No Success yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod"
		 exit 1
	  fi
	done 
	SEARCHPVC=$(kubectl get pvc standard-search-redisgraph-0 -n open-cluster-management -o json | jq '.metadata.name')
	SEARCHPVCBOUND=$(kubectl get pvc standard-search-redisgraph-0 -n open-cluster-management -o json| jq '.status.phase')
	SEARCHPVCSIZE=$(kubectl get pvc standard-search-redisgraph-0 -n open-cluster-management -o json| jq '.spec.resources.requests.storage')

	SEARCHCUSTOMIZATIONS1=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.persistence')
	SEARCHCUSTOMIZATIONS2=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.storageClass')
	SEARCHCUSTOMIZATIONS3=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.storageSize')
	
	if [[ "$SEARCHPVC" == "\"standard-search-redisgraph-0\"" && "$SEARCHPVCBOUND" == "\"Bound\"" && "$SEARCHPVCSIZE" == "\"2Gi\"" ]]
	  then
	      echo "PVC created and bound successfully"
	  else
	      echo "PVC creation or binding failed"
	      exit 1	 
	fi
	if [[ "$SEARCHCUSTOMIZATIONS1" == "true" && "$SEARCHCUSTOMIZATIONS2" == "\"standard\"" && "$SEARCHCUSTOMIZATIONS3" == "\"2Gi\"" ]]
	  then
	      echo "STATUS verified in SearchCustomization"
	  else
	      echo "STATUS verification Failed in SearchCustomization"
	      exit 1	 
	fi
}

test_invalid_storageclass() {
	echo "=====Update searchcustomization with invalid storage class ====="
	# echo -n "Patch searchcustomization: " && kubectl patch searchcustomization searchcustomization -p '{"spec":{"persistence":false}}' --type='merge'
	echo -n "Patch searchcustomization: " && kubectl patch searchcustomization searchcustomization -p '{"spec":{"storageClass": "test"}}' --type='merge'
	echo "Waiting 4 minutes for the redisgraph pod to get Ready... " && sleep 240
	count=0
	while true ; do
	  SEARCHOPERATOR=$(kubectl get searchoperator searchoperator -n open-cluster-management -o json | jq '.status.persistence')
	  echo $SEARCHOPERATOR
	  count=`expr $count + 1`
	  if [[ "$SEARCHOPERATOR" == "\"Unable to create Redisgraph Deployment using PVC\"" ]]
	  then
	     echo "SUCCESS - Testing invalid storageclass setting works"
		 break
	  fi
	  echo "No Success yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up invalid storageclass"
		 exit 1
	  fi
	done 
	SEARCHPVC=$(kubectl get pvc test-search-redisgraph-0 -n open-cluster-management -o json | jq '.metadata.name')
	SEARCHPVCBOUND=$(kubectl get pvc test-search-redisgraph-0 -n open-cluster-management -o json| jq '.status.phase')
	SEARCHCUSTOMIZATIONS1=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.persistence')
	SEARCHCUSTOMIZATIONS2=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.storageClass')
	SEARCHCUSTOMIZATIONS3=$(kubectl get searchcustomization searchcustomization -n open-cluster-management -o json | jq '.status.storageSize')
	
	if [[ "$SEARCHPVC" == "\"test-search-redisgraph-0\"" && "$SEARCHPVCBOUND" == "\"Pending\"" ]]
	  then
	      echo "PVC creation attempted successfully with expected status"
	  else
	      echo "PVC creation attempt failed"
	      exit 1	 
	fi
	if [[ "$SEARCHCUSTOMIZATIONS1" == "false" && "$SEARCHCUSTOMIZATIONS2" == "\"\"" && "$SEARCHCUSTOMIZATIONS3" == "\"\"" ]]
	  then
	      echo "STATUS verified in SearchCustomization"
	  else
	      echo "STATUS verification Failed in SearchCustomization"
	      exit 1	 
	fi
}

test_update_podresource_search_operator() {
	echo "=====Update search redisgraph pod memory limit to 5G====="
	echo -n "Patch searchcustomization: " && kubectl patch searchcustomization searchcustomization -p '{"spec":{"persistence":false, "storageClass":""}}' --type='merge'
	echo -n "Delete redisgraph pod : " && kubectl delete pod search-redisgraph-0
	echo -n "Patch searchoperator: " && kubectl patch searchoperator searchoperator -p '{"spec":{"redisgraph_resource":{"limit_memory":"2Gi"}}}' --type='merge'
	echo "Waiting 2 minutes for the redisgraph pod to get Ready... " && sleep 120
    count=0
	while true ; do
	  REDISMEMORYLIMIT=$(kubectl get pod search-redisgraph-0 -n open-cluster-management -o json | jq '.spec.containers[0].resources.limits.memory')
	  echo $REDISMEMORYLIMIT
	  count=`expr $count + 1`
	  if [[ "$REDISMEMORYLIMIT" == "\"2Gi\"" ]]
	  then
	     echo "SUCCESS - Redisgraph Pod Ready with updated memory limit"
		 break
	  fi
	  echo "No Success yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod with updated memory limit"
		 exit 1
	  fi
	done
}

test_fallback_emptydir() {
	echo "=====Delete searchcustomization and test fallbackto EmptyDir====="
	echo -n "Delete searchcustomization: " && kubectl delete searchcustomization searchcustomization 
	echo -n "Patch storageclass: " && kubectl patch sc standard -p '{"metadata":{"annotations":{"storageclass.kubernetes.io/is-default-class":"false"}}}' --type='merge'
	echo -n "Scale statefulset : " && kubectl scale statefulset search-redisgraph --replicas=0
	echo -n "Delete pvc : " && kubectl delete pvc search-redisgraph-pvc-0
	echo -n "Delete statefulset : " && kubectl delete statefulset search-redisgraph
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
	  echo "No Success yet ..Sleeping for 1s"
	  sleep 1s
	  if [ $count -gt 60 ]
	  then
	     echo "FAILED - Setting up Redisgraph Pod"
		 exit 1
	  fi
	done 
}

deploy
