package kernel

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/MixinNetwork/mixin/common"
	"github.com/MixinNetwork/mixin/config"
	"github.com/MixinNetwork/mixin/crypto"
	"github.com/MixinNetwork/mixin/kernel/internal/clock"
	"github.com/MixinNetwork/mixin/logger"
)

func (node *Node) startNewRound(s *common.Snapshot, cache *CacheRound, allowDummy bool) (*FinalRound, bool, error) {
	if s.RoundNumber != cache.Number+1 {
		panic("should never be here")
	}
	final := cache.asFinal()
	if final == nil {
		return nil, false, fmt.Errorf("self cache snapshots not collected yet %s %d", s.NodeId, s.RoundNumber)
	}
	if s.References.Self != final.Hash {
		return nil, false, fmt.Errorf("self cache snapshots not match yet %s %s", s.NodeId, s.References.Self)
	}

	finalized := node.verifyFinalization(s)
	external, err := node.persistStore.ReadRound(s.References.External)
	if err != nil {
		return nil, false, err
	}
	if external == nil && finalized && allowDummy {
		return final, true, nil
	}
	if external == nil {
		return nil, false, fmt.Errorf("external round %s not collected yet", s.References.External)
	}
	if final.NodeId == external.NodeId {
		return nil, false, nil
	}
	if !node.genesisNodesMap[external.NodeId] && external.Number < 7+config.SnapshotReferenceThreshold {
		return nil, false, nil
	}
	if !finalized {
		if external.Number+config.SnapshotSyncRoundThreshold < node.Graph.FinalRound[external.NodeId].Number {
			return nil, false, fmt.Errorf("external reference %s too early %d %d", s.References.External, external.Number, node.Graph.FinalRound[external.NodeId].Number)
		}
		if external.Timestamp > s.Timestamp {
			return nil, false, fmt.Errorf("external reference later than snapshot time %f", time.Duration(external.Timestamp-s.Timestamp).Seconds())
		}
		threshold := external.Timestamp + config.SnapshotReferenceThreshold*config.SnapshotRoundGap*64
		height := uint64(len(node.Graph.RoundHistory[external.NodeId]))
		for id, rounds := range node.Graph.RoundHistory {
			rts, rh := rounds[0].Start, uint64(len(rounds))
			if !node.genesisNodesMap[id] && rounds[0].Number < 7+config.SnapshotReferenceThreshold*2 {
				continue
			}
			if id == s.NodeId || rh < height || rts > s.Timestamp {
				continue
			}
			if rts+config.SnapshotRoundGap*rh > uint64(clock.Now().UnixNano()) {
				continue
			}
			if cr := node.Graph.CacheRound[id]; len(cr.Snapshots) == 0 && cr.Number == rounds[0].Number+1 {
				return nil, false, fmt.Errorf("external reference %s is bare %s:%d", s.References.External, id, rounds[0].Number)
			}
			if threshold < rts {
				return nil, false, fmt.Errorf("external reference %s too early %s:%d %f", s.References.External, id, rounds[0].Number, time.Duration(rts-threshold).Seconds())
			}
		}
	}
	link, err := node.persistStore.ReadLink(s.NodeId, external.NodeId)
	if external.Number < link {
		return nil, false, err
	}
	if external.NodeId == node.IdForNetwork {
		if l := node.Graph.ReverseRoundLinks[s.NodeId]; external.Number < l {
			return nil, false, fmt.Errorf("external reverse reference %s %d %d", s.NodeId, external.Number, l)
		}
		node.Graph.ReverseRoundLinks[s.NodeId] = external.Number
	}
	return final, false, err
}

func (node *Node) assignNewGraphRound(final *FinalRound, cache *CacheRound) {
	if final.NodeId != cache.NodeId {
		panic(fmt.Errorf("should never be here %s %s", final.NodeId, cache.NodeId))
	}
	node.Graph.CacheRound[final.NodeId] = cache
	node.Graph.FinalRound[final.NodeId] = final
	if history := node.Graph.RoundHistory[final.NodeId]; len(history) == 0 && final.Number == 0 {
		node.Graph.RoundHistory[final.NodeId] = append(node.Graph.RoundHistory[final.NodeId], final.Copy())
	} else if n := history[len(history)-1].Number; n > final.Number {
		panic(fmt.Errorf("should never be here %d %d", n, final.Number))
	} else if n+1 < final.Number {
		panic(fmt.Errorf("should never be here %d %d", n, final.Number))
	} else if n+1 == final.Number {
		node.Graph.RoundHistory[final.NodeId] = append(node.Graph.RoundHistory[final.NodeId], final.Copy())
	}
}

func (node *Node) CacheVerify(snap crypto.Hash, sig crypto.Signature, pub crypto.Key) bool {
	key := append(snap[:], sig[:]...)
	key = append(key, pub[:]...)
	hash := "KERNEL:SIGNATURE:" + crypto.NewHash(key).String()
	value := node.cacheStore.Get(nil, []byte(hash))
	if len(value) == 1 {
		return value[0] == byte(1)
	}
	valid := pub.Verify(snap[:], sig)
	if valid {
		node.cacheStore.Set([]byte(hash), []byte{1})
	} else {
		node.cacheStore.Set([]byte(hash), []byte{0})
	}
	return valid
}

func (node *Node) CacheVerifyCosi(snap crypto.Hash, sig *crypto.CosiSignature, publics []*crypto.Key, threshold int) bool {
	if snap.String() == "b3ea56de6124ad2f3ad1d48f2aff8338b761e62bcde6f2f0acba63a32dd8eecc" &&
		sig.String() == "dbb0347be24ecb8de3d66631d347fde724ff92e22e1f45deeb8b5d843fd62da39ca8e39de9f35f1e0f7336d4686917983470c098edc91f456d577fb18069620f000000003fdfe712" {
		// FIXME this is a hack to fix the large round gap around node remove snapshot
		// and a bug in too recent external reference, e.g. bare final round
		return true
	}
	key := common.MsgpackMarshalPanic(sig)
	key = append(snap[:], key...)
	for _, pub := range publics {
		key = append(key, pub[:]...)
	}
	tbuf := make([]byte, 8)
	binary.BigEndian.PutUint64(tbuf, uint64(threshold))
	key = append(key, tbuf...)
	hash := "KERNEL:COSISIGNATURE:" + crypto.NewHash(key).String()
	value := node.cacheStore.Get(nil, []byte(hash))
	if len(value) == 1 {
		return value[0] == byte(1)
	}
	err := sig.FullVerify(publics, threshold, snap[:])
	if err != nil {
		logger.Verbosef("CacheVerifyCosi(%s, %d, %d) ERROR %s\n", snap, len(publics), threshold, err.Error())
		node.cacheStore.Set([]byte(hash), []byte{0})
	} else {
		node.cacheStore.Set([]byte(hash), []byte{1})
	}
	return err == nil
}

func (node *Node) checkInitialAcceptSnapshotWeak(s *common.Snapshot) bool {
	pledge := node.ConsensusPledging
	if pledge == nil {
		return false
	}
	if node.genesisNodesMap[s.NodeId] {
		return false
	}
	if s.NodeId != pledge.IdForNetwork(node.networkId) {
		return false
	}
	return s.RoundNumber == 0
}

func (node *Node) checkInitialAcceptSnapshot(s *common.Snapshot, tx *common.VersionedTransaction) bool {
	if node.Graph.FinalRound[s.NodeId] != nil {
		return false
	}
	return node.checkInitialAcceptSnapshotWeak(s) && tx.TransactionType() == common.TransactionTypeNodeAccept
}

func (node *Node) queueSnapshotOrPanic(peerId crypto.Hash, s *common.Snapshot) error {
	err := node.persistStore.QueueAppendSnapshot(peerId, s, false)
	if err != nil {
		panic(err)
	}
	return nil
}

func (node *Node) clearAndQueueSnapshotOrPanic(s *common.Snapshot) error {
	delete(node.CosiVerifiers, s.Hash)
	node.CosiAggregators.Delete(s.Hash)
	node.CosiAggregators.Delete(s.Transaction)
	return node.queueSnapshotOrPanic(node.IdForNetwork, &common.Snapshot{
		Version:     common.SnapshotVersion,
		NodeId:      s.NodeId,
		Transaction: s.Transaction,
	})
}

func (node *Node) verifyFinalization(s *common.Snapshot) bool {
	if s.Version == 0 {
		return node.legacyVerifyFinalization(s.Timestamp, s.Signatures)
	}
	if s.Version != common.SnapshotVersion || s.Signature == nil {
		return false
	}
	publics := node.ConsensusKeys(s.Timestamp)
	if node.checkInitialAcceptSnapshotWeak(s) {
		publics = append(publics, &node.ConsensusPledging.Signer.PublicSpendKey)
	}
	base := node.ConsensusThreshold(s.Timestamp)
	if node.CacheVerifyCosi(s.PayloadHash(), s.Signature, publics, base) {
		return true
	}
	if rr := node.ConsensusRemovedRecently(s.Timestamp); rr != nil {
		for i := range publics {
			pwr := append([]*crypto.Key{}, publics[:i]...)
			pwr = append(pwr, &rr.Signer.PublicSpendKey)
			pwr = append(pwr, publics[i:]...)
			if node.CacheVerifyCosi(s.PayloadHash(), s.Signature, pwr, base) {
				return true
			}
		}
	}
	return false
}

func (node *Node) legacyVerifyFinalization(timestamp uint64, sigs []*crypto.Signature) bool {
	return len(sigs) >= node.ConsensusThreshold(timestamp)
}
