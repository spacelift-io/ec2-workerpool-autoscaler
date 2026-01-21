package ifaces

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// AzureKeyVault is an interface for the Azure Key Vault client.
//
//go:generate mockery --output ./ --name AzureKeyVault --filename mock_azure_keyvault.go --outpkg ifaces --structname MockAzureKeyVault
type AzureKeyVault interface {
	GetSecret(ctx context.Context, secretName string) (azsecrets.GetSecretResponse, error)
}
