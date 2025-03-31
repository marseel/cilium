// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package reconcilerv2

import (
	"errors"
	"sort"

	"github.com/cilium/hive/cell"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	"github.com/cilium/cilium/pkg/bgpv1/manager/store"
	"github.com/cilium/cilium/pkg/bgpv1/types"
	"github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2alpha1"
	"github.com/cilium/cilium/pkg/k8s/resource"
	"github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/labels"
	slim_metav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
)

// PeerID identifies the peer within the instance.
type PeerID struct {
	Name    string
	Address string
}

type (
	// PeerAdvertisements is a map of peer name to its family advertisements
	// This is the top level map that is returned to the consumer with requested advertisements.
	PeerAdvertisements       map[PeerID]PeerFamilyAdvertisements
	PeerFamilyAdvertisements map[v2alpha1.CiliumBGPFamily][]v2alpha1.BGPAdvertisement // key is the address family type
)

type PeerAdvertisementIn struct {
	cell.In

	Logger          logrus.FieldLogger
	PeerConfigStore store.BGPCPResourceStore[*v2alpha1.CiliumBGPPeerConfig]
	AdvertStore     store.BGPCPResourceStore[*v2alpha1.CiliumBGPAdvertisement]
}

type CiliumPeerAdvertisement struct {
	logger     logrus.FieldLogger
	peerConfig store.BGPCPResourceStore[*v2alpha1.CiliumBGPPeerConfig]
	adverts    store.BGPCPResourceStore[*v2alpha1.CiliumBGPAdvertisement]
}

func NewCiliumPeerAdvertisement(p PeerAdvertisementIn) *CiliumPeerAdvertisement {
	return &CiliumPeerAdvertisement{
		logger:     p.Logger,
		peerConfig: p.PeerConfigStore,
		adverts:    p.AdvertStore,
	}
}

// GetConfiguredAdvertisements can be called to get all configured advertisements of given BGPAdvertisementType for each peer.
// Advertisements are selected based on below criteria:
// Each peer is selected from the BGP node instance configuration. For each peer, the peer configuration is fetched
// from local store.
// Peer configuration contains the list of families and the advertisement selector.
// We iterate over all advertisements ( available from local store ), select only those that match the advertisement
// selector of the family.
// Information of peer -> family -> advertisements is returned to the consumer.
// Linear scan [ Peers ] - O(n) ( number of peers )
// Linear scan [ Families ] - O(m) ( max 2 )
// Linear scan [ Advertisements ] - O(k) ( number of advertisements - 3-4 types, which is again filtered)
func (p *CiliumPeerAdvertisement) GetConfiguredAdvertisements(conf *v2alpha1.CiliumBGPNodeInstance, selectAdvertTypes ...v2alpha1.BGPAdvertisementType) (PeerAdvertisements, error) {
	result := make(PeerAdvertisements)
	l := p.logger.WithField(types.InstanceLogField, conf.Name)
	for _, peer := range conf.Peers {
		lp := l.WithField(types.PeerLogField, peer.Name)

		if peer.PeerConfigRef == nil || peer.PeerConfigRef.Name == "" {
			lp.Debug("Peer config not specified, skipping advertisement check")
			continue
		}
		peerConfig, exist, err := p.peerConfig.GetByKey(resource.Key{Name: peer.PeerConfigRef.Name})
		if err != nil {
			if errors.Is(err, store.ErrStoreUninitialized) {
				lp.Errorf("BUG: Peer config store is not initialized")
			}
			return nil, err
		}
		if !exist {
			lp.Debug("Peer config not found, skipping advertisement check")
			continue
		}

		peerAdverts, err := p.getPeerAdvertisements(peerConfig, selectAdvertTypes...)
		if err != nil {
			return nil, err
		}
		id := PeerID{
			Name:    peer.Name,
			Address: ptr.Deref(peer.PeerAddress, ""),
		}
		result[id] = peerAdverts
	}
	return result, nil
}

func (p *CiliumPeerAdvertisement) getPeerAdvertisements(peerConfig *v2alpha1.CiliumBGPPeerConfig, selectAdvertTypes ...v2alpha1.BGPAdvertisementType) (PeerFamilyAdvertisements, error) {
	result := make(map[v2alpha1.CiliumBGPFamily][]v2alpha1.BGPAdvertisement)

	for _, family := range peerConfig.Spec.Families {
		advert, err := p.getFamilyAdvertisements(family, selectAdvertTypes...)
		if err != nil {
			return result, err
		}
		result[family.CiliumBGPFamily] = advert
	}
	return result, nil
}

func (p *CiliumPeerAdvertisement) getFamilyAdvertisements(family v2alpha1.CiliumBGPFamilyWithAdverts, selectAdvertTypes ...v2alpha1.BGPAdvertisementType) ([]v2alpha1.BGPAdvertisement, error) {
	// get all advertisement CRD objects.
	advertResources, err := p.adverts.List()
	if err != nil {
		return nil, err
	}

	// select only label selected advertisements for the family
	selectedAdvertResources, err := p.familySelectedAdvertisements(family, advertResources)
	if err != nil {
		return nil, err
	}

	// create selectTypeSet for easier lookup
	selectTypesSet := sets.New[string]()
	for _, selectType := range selectAdvertTypes {
		selectTypesSet.Insert(string(selectType))
	}

	var selectedAdvertisements []v2alpha1.BGPAdvertisement
	// select advertisements requested by the consumer
	for _, advertResource := range selectedAdvertResources {
		for _, advert := range advertResource.Spec.Advertisements {
			// check if the advertisement type is in the selectType set
			if selectTypesSet.Has(string(advert.AdvertisementType)) {
				selectedAdvertisements = append(selectedAdvertisements, advert)
			}
		}
	}

	return selectedAdvertisements, nil
}

func (p *CiliumPeerAdvertisement) familySelectedAdvertisements(family v2alpha1.CiliumBGPFamilyWithAdverts, adverts []*v2alpha1.CiliumBGPAdvertisement) ([]*v2alpha1.CiliumBGPAdvertisement, error) {
	var result []*v2alpha1.CiliumBGPAdvertisement
	advertSelector, err := slim_metav1.LabelSelectorAsSelector(family.Advertisements)
	if err != nil {
		return nil, err
	}

	for _, advert := range adverts {
		if advertSelector.Matches(labels.Set(advert.Labels)) {
			result = append(result, advert)
		}
	}
	return result, nil
}

func PeerAdvertisementsEqual(first, second PeerAdvertisements) bool {
	if len(first) != len(second) {
		return false
	}

	for peer, peerAdverts := range first {
		if !FamilyAdvertisementsEqual(peerAdverts, second[peer]) {
			return false
		}
	}
	return true
}

func FamilyAdvertisementsEqual(first, second PeerFamilyAdvertisements) bool {
	if len(first) != len(second) {
		return false
	}

	for family, familyAdverts := range first {
		otherFamilyAdverts, exist := second[family]
		if !exist || len(familyAdverts) != len(otherFamilyAdverts) {
			return false
		}

		sort.Slice(familyAdverts, func(i, j int) bool {
			return familyAdverts[i].AdvertisementType < familyAdverts[j].AdvertisementType
		})

		sort.Slice(otherFamilyAdverts, func(i, j int) bool {
			return otherFamilyAdverts[i].AdvertisementType < otherFamilyAdverts[j].AdvertisementType
		})

		for i, advert := range familyAdverts {
			if !advert.DeepEqual(&otherFamilyAdverts[i]) {
				return false
			}
		}
	}
	return true
}
