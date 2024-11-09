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

package cacheserviceaccounttokens

import (
	"context"
	"fmt"

	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
)

type serviceAccount struct {
	serviceaccounts.Reference
	podCount         int
	nodeIsUsing      bool
	deleted          bool
	tokens           *tokens
	externalRequests chan chan<- *tokensAndError
}

func (p *Provider) getTokens(ctx context.Context, ref *serviceaccounts.Reference) (*tokens, error) {
	p.serviceAccountsMutex.Lock()
	sa, ok := p.serviceAccounts[*ref]
	if !ok {
		const podCount = 0
		const nodeIsUsing = false
		sa = p.addServiceAccount(ref, podCount, nodeIsUsing)
	} else if sa.deleted {
		p.serviceAccountsMutex.Unlock()
		return nil, errServiceAccountDeleted
	}
	p.serviceAccountsMutex.Unlock()

	tokens := sa.tokens
	if tokens == nil || tokens.serviceAccountToken.isExpired() || tokens.googleAccessToken.isExpired() {
		p.cacheMisses.Inc()
		tokens, err := sa.requestTokens(ctx, p.ctx)
		if err != nil {
			return nil, err
		}
		return tokens, nil
	}

	return tokens, nil
}

func (s *serviceAccount) requestTokens(reqCtx, providerCtx context.Context) (*tokens, error) {
	req := make(chan *tokensAndError, 1)

	select {
	case s.externalRequests <- req:
	case <-reqCtx.Done():
		close(req)
		return nil, fmt.Errorf("request context done while dispatching request for service account tokens: %w",
			reqCtx.Err())
	case <-providerCtx.Done():
		close(req)
		return nil, fmt.Errorf("process terminated while dispatching request for service account tokens: %w",
			providerCtx.Err())
	}

	select {
	case resp := <-req:
		if resp.err != nil {
			return nil, resp.err
		}
		return resp.tokens, nil
	case <-reqCtx.Done():
		return nil, fmt.Errorf("request context done while waiting response with service account tokens: %w",
			reqCtx.Err())
	case <-providerCtx.Done():
		return nil, fmt.Errorf("process terminated while waiting response with service account tokens: %w",
			providerCtx.Err())
	}
}

func (p *Provider) addServiceAccount(ref *serviceaccounts.Reference, podCount int, nodeIsUsing bool) *serviceAccount {
	sa := &serviceAccount{
		Reference:        *ref,
		podCount:         podCount,
		nodeIsUsing:      nodeIsUsing,
		externalRequests: make(chan chan<- *tokensAndError, 1),
	}
	p.serviceAccounts[sa.Reference] = sa
	p.numTokens.Inc()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.cacheTokens(sa)
	}()

	return sa
}

func (p *Provider) AddPodServiceAccount(ref *serviceaccounts.Reference) {
	p.serviceAccountsMutex.Lock()
	defer p.serviceAccountsMutex.Unlock()

	if sa, ok := p.serviceAccounts[*ref]; ok {
		sa.podCount++
		return
	}

	const podCount = 1
	const nodeIsUsing = false
	p.addServiceAccount(ref, podCount, nodeIsUsing)
}

func (p *Provider) DeletePodServiceAccount(ref *serviceaccounts.Reference) {
	p.serviceAccountsMutex.Lock()
	defer p.serviceAccountsMutex.Unlock()

	if sa, ok := p.serviceAccounts[*ref]; ok && sa.podCount > 0 {
		sa.podCount--
	}
}

func (p *Provider) UpdateNodeServiceAccount(ref *serviceaccounts.Reference) {
	p.serviceAccountsMutex.Lock()
	defer p.serviceAccountsMutex.Unlock()

	if cur := p.nodeServiceAccountRef; cur != nil {
		if *cur == *ref {
			return
		}
		if sa, ok := p.serviceAccounts[*cur]; ok {
			sa.nodeIsUsing = false
		}
	}
	p.nodeServiceAccountRef = ref

	if sa, ok := p.serviceAccounts[*ref]; ok {
		sa.nodeIsUsing = true
		return
	}

	const podCount = 0
	const nodeIsUsing = true
	p.addServiceAccount(ref, podCount, nodeIsUsing)
}

func (p *Provider) UpdateServiceAccount(ref *serviceaccounts.Reference) {
	p.serviceAccountsMutex.Lock()
	defer p.serviceAccountsMutex.Unlock()

	sa, ok := p.serviceAccounts[*ref]
	if !ok {
		return
	}
	sa.deleted = false

	select {
	case sa.externalRequests <- nil:
	default:
	}
}

func (p *Provider) DeleteServiceAccount(ref *serviceaccounts.Reference) {
	p.serviceAccountsMutex.Lock()
	defer p.serviceAccountsMutex.Unlock()

	sa, ok := p.serviceAccounts[*ref]
	if !ok {
		return
	}
	sa.deleted = true

	select {
	case sa.externalRequests <- nil:
	default:
	}
}

func (p *Provider) checkIfMustDeleteAndDelete(sa *serviceAccount) bool {
	p.serviceAccountsMutex.Lock()
	defer p.serviceAccountsMutex.Unlock()

	if (sa.podCount == 0 && !sa.nodeIsUsing) || sa.deleted {
		delete(p.serviceAccounts, sa.Reference)
		p.numTokens.Dec()
		return true
	}

	return false
}
