package internal_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shurcooL/graphql"
	"github.com/stretchr/testify/require"

	"github.com/spacelift-io/awsautoscalr/internal"
)

// The drain mutation is built by the same GraphQL client that derives its
// selection set from struct tags. It must not request availableAt: self-hosted
// backends whose schema predates that field reject the whole operation
// ("Cannot query field \"availableAt\" on type \"Worker\"."), and the runtime
// nil-check fallback never runs because no response is returned at all.
func TestWorkerDrainSetMutation_DoesNotSelectAvailableAt(t *testing.T) {
	var capturedBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		// Response omits availableAt so it decodes against both the old (Worker)
		// and fixed (WorkerLegacy) mutation return types.
		_, _ = io.WriteString(w, `{"data":{"workerDrainSet":{"id":"w1","busy":false,"createdAt":0,"drained":true,"metadata":""}}}`)
	}))
	defer srv.Close()

	client := graphql.NewClient(srv.URL, srv.Client())

	err := client.Mutate(context.Background(), &internal.WorkerDrainSet{}, map[string]any{
		"workerPoolId": graphql.ID("wp1"),
		"workerId":     graphql.ID("w1"),
		"drain":        graphql.Boolean(true),
	})

	require.NoError(t, err)
	require.Contains(t, capturedBody, "workerDrainSet", "sanity: request should carry the drain mutation")
	require.NotContains(t, capturedBody, "availableAt", "drain mutation must not select availableAt (breaks pre-availableAt self-hosted backends)")
}
