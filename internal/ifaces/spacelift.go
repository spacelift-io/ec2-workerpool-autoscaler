package ifaces

import (
	"context"

	"github.com/shurcooL/graphql"
)

// Spacelift is an interface which mocks the subset of the Spacelift client that
// we use in the controller.
//
//go:generate mockery --inpackage --name Spacelift --filename mock_spacelift.go
type Spacelift interface {
	Query(context.Context, interface{}, map[string]interface{}, ...graphql.RequestOption) error
	Mutate(context.Context, interface{}, map[string]interface{}, ...graphql.RequestOption) error
}
