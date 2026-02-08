//go:build integration

package main

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	namespacesv1 "github.com/RynoXLI/Wayfile/gen/go/namespaces/v1"
)

// TestNamespaceCRUD tests the namespace CRUD operations via Connect RPC
func TestNamespaceCRUD(t *testing.T) {
	ta := SetupTestApp(t)
	defer ta.Cleanup(t)

	ctx := context.Background()

	// === Step 1: Create a namespace ===
	createResp, err := ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.NoError(t, err)
	require.NotNil(t, createResp)
	require.NotNil(t, createResp.Namespace)
	require.Equal(t, "test-namespace-1", createResp.Namespace.Name)
	require.NotEmpty(t, createResp.Namespace.Id)
	require.NotNil(t, createResp.Namespace.CreatedAt)
	require.NotNil(t, createResp.Namespace.ModifiedAt)

	// === Step 2: Create another namespace ===
	createResp2, err := ta.NamespaceClient.CreateNamespace(
		ctx,
		&namespacesv1.CreateNamespaceRequest{
			Name: "test-namespace-2",
		},
	)
	require.NoError(t, err)
	require.NotNil(t, createResp2)
	require.NotNil(t, createResp2.Namespace)
	require.Equal(t, "test-namespace-2", createResp2.Namespace.Name)

	// === Step 3: Get all namespaces ===
	listResp, err := ta.NamespaceClient.ListNamespaces(ctx, &namespacesv1.ListNamespacesRequest{})
	require.NoError(t, err)
	require.NotNil(t, listResp)
	require.GreaterOrEqual(t, len(listResp.Namespaces), 2, "Should have at least 2 namespaces")

	// Check that our namespaces are in the list
	foundNs1 := false
	foundNs2 := false
	for _, ns := range listResp.Namespaces {
		if ns.Name == "test-namespace-1" {
			foundNs1 = true
		}
		if ns.Name == "test-namespace-2" {
			foundNs2 = true
		}
	}
	require.True(t, foundNs1, "test-namespace-1 should be in the list")
	require.True(t, foundNs2, "test-namespace-2 should be in the list")

	// === Step 4: Get specific namespace by name ===
	getResp, err := ta.NamespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp)
	require.NotNil(t, getResp.Namespace)
	require.Equal(t, "test-namespace-1", getResp.Namespace.Name)
	require.Equal(t, createResp.Namespace.Id, getResp.Namespace.Id)

	// === Step 5: Try to get non-existent namespace ===
	_, err = ta.NamespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "nonexistent",
	})
	require.Error(t, err)
	var connectErr *connect.Error
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeNotFound,
		connectErr.Code(),
		"Non-existent namespace should return NotFound",
	)

	// === Step 6: Delete a namespace ===
	deleteResp, err := ta.NamespaceClient.DeleteNamespace(ctx, &namespacesv1.DeleteNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.NoError(t, err)
	require.NotNil(t, deleteResp)

	// === Step 7: Verify namespace was deleted ===
	_, err = ta.NamespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "test-namespace-1",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeNotFound,
		connectErr.Code(),
		"Deleted namespace should not be found",
	)

	// === Step 8: Verify other namespace still exists ===
	getResp2, err := ta.NamespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "test-namespace-2",
	})
	require.NoError(t, err)
	require.NotNil(t, getResp2)
	require.NotNil(t, getResp2.Namespace)
	require.Equal(t, "test-namespace-2", getResp2.Namespace.Name)

	// === Step 9: Test validation - empty namespace name on create ===
	_, err = ta.NamespaceClient.CreateNamespace(ctx, &namespacesv1.CreateNamespaceRequest{
		Name: "",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty name should return InvalidArgument",
	)

	// === Step 10: Test validation - empty namespace name on get ===
	_, err = ta.NamespaceClient.GetNamespace(ctx, &namespacesv1.GetNamespaceRequest{
		Name: "",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty name should return InvalidArgument",
	)

	// === Step 11: Test validation - empty namespace name on delete ===
	_, err = ta.NamespaceClient.DeleteNamespace(ctx, &namespacesv1.DeleteNamespaceRequest{
		Name: "",
	})
	require.Error(t, err)
	require.ErrorAs(t, err, &connectErr)
	require.Equal(
		t,
		connect.CodeInvalidArgument,
		connectErr.Code(),
		"Empty name should return InvalidArgument",
	)
}
