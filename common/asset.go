package common

import (
	"fmt"

	"github.com/MixinNetwork/mixin/crypto"
	"github.com/MixinNetwork/mixin/domains/ethereum"
	"github.com/MixinNetwork/mixin/domains/tron"
)

var (
	EthereumChainId crypto.Hash
	TronChainId     crypto.Hash

	XINAssetId crypto.Hash
)

type Asset struct {
	ChainId  crypto.Hash
	AssetKey string
}

func init() {
	EthereumChainId = crypto.NewHash([]byte("43d61dcd-e413-450d-80b8-101d5e903357"))
	TronChainId = crypto.NewHash([]byte("25dabac5-056a-48ff-b9f9-f67395dc407c"))

	XINAssetId = crypto.NewHash([]byte("c94ac88f-4671-3976-b60a-09064f1811e8"))
}

func (a *Asset) Verify() error {
	switch a.ChainId {
	case EthereumChainId:
		return ethereum.VerifyAssetKey(a.AssetKey)
	case TronChainId:
		return tron.VerifyAssetKey(a.AssetKey)
	default:
		return fmt.Errorf("invalid chain id %s", a.ChainId)
	}
}

func (a *Asset) AssetId() crypto.Hash {
	switch a.ChainId {
	case EthereumChainId:
		return ethereum.GenerateAssetId(a.AssetKey)
	case TronChainId:
		return tron.GenerateAssetId(a.AssetKey)
	default:
		return crypto.Hash{}
	}
}

func (a *Asset) FeeAssetId() crypto.Hash {
	switch a.ChainId {
	case EthereumChainId:
		return EthereumChainId
	case TronChainId:
		return TronChainId
	}
	return crypto.Hash{}
}
