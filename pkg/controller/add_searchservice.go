package controller

import (
	"github.com/open-cluster-management/search-operator/pkg/controller/searchservice"
)

func init() {
	// AddToManagerFuncs is a list of functions to create controllers and add them to a manager.
	AddToManagerFuncs = append(AddToManagerFuncs, searchservice.Add)
}
