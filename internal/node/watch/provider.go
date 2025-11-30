// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package watchnode

import (
	"context"
	"fmt"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/node"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	informersv1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type (
	Provider struct {
		opts          ProviderOptions
		closeChannel  chan struct{}
		closedChannel chan struct{}
		informer      cache.SharedIndexInformer
		listeners     []Listener
	}

	ProviderOptions struct {
		NodeName       string
		FallbackSource node.Provider
		KubeClient     *kubernetes.Clientset
		ResyncPeriod   time.Duration
	}

	Listener interface {
		UpdateNodeServiceAccount(*serviceaccounts.Reference)
	}
)

func NewProvider(opts ProviderOptions) *Provider {
	informer := informersv1.NewFilteredNodeInformer(
		opts.KubeClient,
		opts.ResyncPeriod,
		cache.Indexers{},
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + opts.NodeName
		},
	)

	p := &Provider{
		opts:          opts,
		closeChannel:  make(chan struct{}),
		closedChannel: make(chan struct{}),
		informer:      informer,
	}

	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			saRef := serviceaccounts.ReferenceFromNode(obj.(*corev1.Node))
			for _, l := range p.listeners {
				l.UpdateNodeServiceAccount(saRef)
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			saRef := serviceaccounts.ReferenceFromNode(newObj.(*corev1.Node))
			for _, l := range p.listeners {
				l.UpdateNodeServiceAccount(saRef)
			}
		},
	})

	return p
}

func (p *Provider) Get(ctx context.Context) (*corev1.Node, error) {
	node, err := p.get()
	if err == nil {
		return node, nil
	}
	if p.opts.FallbackSource == nil {
		return nil, fmt.Errorf("error getting node from cache: %w", err)
	}

	logging.
		FromContext(ctx).
		WithError(err).
		Error("error getting node from cache, delegating request to fallback source")

	node, err = p.opts.FallbackSource.Get(ctx)
	if err != nil {
		return nil, err
	}
	return node, nil
}

func (p *Provider) get() (*corev1.Node, error) {
	v, ok, err := p.informer.GetStore().GetByKey(p.opts.NodeName)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("node not present in cache")
	}
	return v.(*corev1.Node), nil
}

func (p *Provider) Start(ctx context.Context) {
	go func() {
		logging.FromContext(ctx).Info("starting watch node...")
		p.informer.Run(p.closeChannel)
		close(p.closedChannel)
	}()
}

func (p *Provider) Close() error {
	close(p.closeChannel)
	<-p.closedChannel
	return nil
}

func (p *Provider) AddListener(l Listener) {
	p.listeners = append(p.listeners, l)
}
