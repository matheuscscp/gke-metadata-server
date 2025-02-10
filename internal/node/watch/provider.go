// MIT License
//
// Copyright (c) 2024 Matheus Pimenta
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package watchnode

import (
	"context"
	"fmt"
	"time"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/node"

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
	}

	ProviderOptions struct {
		NodeName       string
		FallbackSource node.Provider
		KubeClient     *kubernetes.Clientset
		ResyncPeriod   time.Duration
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
