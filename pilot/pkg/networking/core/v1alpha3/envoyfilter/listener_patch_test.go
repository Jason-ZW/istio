// Copyright 2018 Istio Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package envoyfilter

import (
	"fmt"
	"strings"
	"testing"

	xdsapi "github.com/envoyproxy/go-control-plane/envoy/api/v2"
	"github.com/envoyproxy/go-control-plane/envoy/api/v2/auth"
	"github.com/envoyproxy/go-control-plane/envoy/api/v2/core"
	"github.com/envoyproxy/go-control-plane/envoy/api/v2/listener"
	http_conn "github.com/envoyproxy/go-control-plane/envoy/config/filter/network/http_connection_manager/v2"
	xdsutil "github.com/envoyproxy/go-control-plane/pkg/util"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/types"
	"github.com/google/go-cmp/cmp"

	meshconfig "istio.io/api/mesh/v1alpha1"
	networking "istio.io/api/networking/v1alpha3"
	"istio.io/istio/pilot/pkg/model"
	"istio.io/istio/pilot/pkg/networking/core/v1alpha3/fakes"
	"istio.io/istio/pilot/pkg/networking/util"
)

var (
	testMesh = meshconfig.MeshConfig{
		ConnectTimeout: &types.Duration{
			Seconds: 10,
			Nanos:   1,
		},
	}
)

func buildEnvoyFilterConfigStore(configPatches []*networking.EnvoyFilter_EnvoyConfigObjectPatch) *fakes.IstioConfigStore {
	return &fakes.IstioConfigStore{
		ListStub: func(typ, namespace string) (configs []model.Config, e error) {
			if typ == "envoy-filter" {
				// to emulate returning multiple envoy filter configs
				for i, cp := range configPatches {
					configs = append(configs, model.Config{
						ConfigMeta: model.ConfigMeta{
							Name:      fmt.Sprintf("test-envoyfilter-%d", i),
							Namespace: "not-default",
						},
						Spec: &networking.EnvoyFilter{
							ConfigPatches: []*networking.EnvoyFilter_EnvoyConfigObjectPatch{cp},
						},
					})
				}
			}
			return
		},
	}
}

func buildPatchStruct(config string) *types.Struct {
	val := &types.Struct{}
	jsonpb.Unmarshal(strings.NewReader(config), val)
	return val
}

func newTestEnvironment(serviceDiscovery model.ServiceDiscovery, mesh meshconfig.MeshConfig,
	configStore model.IstioConfigStore) *model.Environment {
	env := &model.Environment{
		ServiceDiscovery: serviceDiscovery,
		IstioConfigStore: configStore,
		Mesh:             &mesh,
	}

	env.PushContext = model.NewPushContext()
	_ = env.PushContext.InitContext(env)

	return env
}

func TestApplyListenerPatches(t *testing.T) {
	configPatches := []*networking.EnvoyFilter_EnvoyConfigObjectPatch{
		{
			ApplyTo: networking.EnvoyFilter_LISTENER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_SIDECAR_OUTBOUND,
				Proxy: &networking.EnvoyFilter_ProxyMatch{
					Metadata: map[string]string{"foo": "sidecar"},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_ADD,
				Value:     buildPatchStruct(`{"name":"new-outbound-listener1"}`),
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_NETWORK_FILTER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_SIDECAR_OUTBOUND,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 12345,
						FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{
							Filter: &networking.EnvoyFilter_ListenerMatch_FilterMatch{Name: "filter1"},
						},
					},
				},
				Proxy: &networking.EnvoyFilter_ProxyMatch{
					ProxyVersion: `^1\.[2-9](.*?)$`,
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_INSERT_BEFORE,
				Value:     buildPatchStruct(`{"name":"filter0"}`),
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_NETWORK_FILTER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_SIDECAR_OUTBOUND,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 12345,
						FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{
							Filter: &networking.EnvoyFilter_ListenerMatch_FilterMatch{Name: "filter2"},
						},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_REMOVE,
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_LISTENER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_SIDECAR_INBOUND,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 12345,
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_REMOVE,
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_LISTENER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_SIDECAR_INBOUND,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 80,
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_MERGE,
				Value:     buildPatchStruct(`{listener_filters: nil}`),
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_FILTER_CHAIN,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_SIDECAR_INBOUND,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber:  80,
						FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{TransportProtocol: "tls"},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_REMOVE,
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_LISTENER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_GATEWAY,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 80,
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_MERGE,
				Value:     buildPatchStruct(`{"listener_filters": [{"name":"foo"}]}`),
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_FILTER_CHAIN,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_GATEWAY,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 80,
						FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{
							Sni: "*.foo.com",
						},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_MERGE,
				Value:     buildPatchStruct(`{"filter_chain_match": { "server_names": ["foo.com"] }}`),
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_HTTP_FILTER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_GATEWAY,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 80,
						FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{
							Sni: "*.foo.com",
							Filter: &networking.EnvoyFilter_ListenerMatch_FilterMatch{
								Name:      xdsutil.HTTPConnectionManager,
								SubFilter: &networking.EnvoyFilter_ListenerMatch_SubFilterMatch{Name: "http-filter2"},
							},
						},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_INSERT_AFTER,
				Value:     buildPatchStruct(`{"name": "http-filter3"}`),
			},
		},
		{
			ApplyTo: networking.EnvoyFilter_HTTP_FILTER,
			Match: &networking.EnvoyFilter_EnvoyConfigObjectMatch{
				Context: networking.EnvoyFilter_SIDECAR_INBOUND,
				ObjectTypes: &networking.EnvoyFilter_EnvoyConfigObjectMatch_Listener{
					Listener: &networking.EnvoyFilter_ListenerMatch{
						PortNumber: 80,
						FilterChain: &networking.EnvoyFilter_ListenerMatch_FilterChainMatch{
							Filter: &networking.EnvoyFilter_ListenerMatch_FilterMatch{
								Name:      xdsutil.HTTPConnectionManager,
								SubFilter: &networking.EnvoyFilter_ListenerMatch_SubFilterMatch{Name: "http-filter2"},
							},
						},
					},
				},
			},
			Patch: &networking.EnvoyFilter_Patch{
				Operation: networking.EnvoyFilter_Patch_INSERT_BEFORE,
				Value:     buildPatchStruct(`{"name": "http-filter3"}`),
			},
		},
	}

	sidecarOutboundIn := []*xdsapi.Listener{
		{
			Name: "12345",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 12345,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{
				{
					Filters: []*listener.Filter{
						{Name: "filter1"},
						{Name: "filter2"},
					},
				},
			},
		},
		{
			Name: "another-listener",
		},
	}

	sidecarOutboundOut := []*xdsapi.Listener{
		{
			Name: "12345",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 12345,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{
				{
					Filters: []*listener.Filter{
						{Name: "filter0"},
						{Name: "filter1"},
					},
				},
			},
		},
		{
			Name: "another-listener",
		},
		{
			Name: "new-outbound-listener1",
		},
	}

	sidecarOutboundInNoAdd := []*xdsapi.Listener{
		{
			Name: "12345",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 12345,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{
				{
					Filters: []*listener.Filter{
						{Name: "filter1"},
						{Name: "filter2"},
					},
				},
			},
		},
		{
			Name: "another-listener",
		},
	}

	sidecarOutboundOutNoAdd := []*xdsapi.Listener{
		{
			Name: "12345",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 12345,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{
				{
					Filters: []*listener.Filter{
						{Name: "filter0"},
						{Name: "filter1"},
					},
				},
			},
		},
		{
			Name: "another-listener",
		},
	}

	sidecarInboundIn := []*xdsapi.Listener{
		{
			Name: "12345",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 12345,
						},
					},
				},
			},
		},
		{
			Name: "another-listener",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 80,
						},
					},
				},
			},
			ListenerFilters: []*listener.ListenerFilter{{Name: "envoy.tls_inspector"}},
			FilterChains: []*listener.FilterChain{
				{
					FilterChainMatch: &listener.FilterChainMatch{TransportProtocol: "tls"},
					TlsContext:       &auth.DownstreamTlsContext{},
					Filters:          []*listener.Filter{{Name: "network-filter"}},
				},
				{
					Filters: []*listener.Filter{
						{
							Name: xdsutil.HTTPConnectionManager,
							ConfigType: &listener.Filter_TypedConfig{
								TypedConfig: util.MessageToAny(&http_conn.HttpConnectionManager{
									HttpFilters: []*http_conn.HttpFilter{
										{Name: "http-filter1"},
										{Name: "http-filter2"},
									},
								}),
							},
						},
					},
				},
			},
		},
	}

	sidecarInboundOut := []*xdsapi.Listener{
		{
			Name: "another-listener",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 80,
						},
					},
				},
			},
			ListenerFilters: []*listener.ListenerFilter{{Name: "envoy.tls_inspector"}},
			FilterChains: []*listener.FilterChain{
				{
					Filters: []*listener.Filter{
						{
							Name: xdsutil.HTTPConnectionManager,
							ConfigType: &listener.Filter_TypedConfig{
								TypedConfig: util.MessageToAny(&http_conn.HttpConnectionManager{
									HttpFilters: []*http_conn.HttpFilter{
										{Name: "http-filter1"},
										{Name: "http-filter3"},
										{Name: "http-filter2"},
									},
								}),
							},
						},
					},
				},
			},
		},
	}

	gatewayIn := []*xdsapi.Listener{
		{
			Name: "80",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 80,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{
				{
					FilterChainMatch: &listener.FilterChainMatch{
						ServerNames: []string{"match.com", "*.foo.com"},
					},
					Filters: []*listener.Filter{
						{
							Name: xdsutil.HTTPConnectionManager,
							ConfigType: &listener.Filter_TypedConfig{
								TypedConfig: util.MessageToAny(&http_conn.HttpConnectionManager{
									HttpFilters: []*http_conn.HttpFilter{
										{Name: "http-filter1"},
										{Name: "http-filter2"},
									},
								}),
							},
						},
					},
				},
			},
		},
		{
			Name: "another-listener",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 443,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{
				{
					FilterChainMatch: &listener.FilterChainMatch{
						ServerNames: []string{"nomatch.com", "*.foo.com"},
					},
					Filters: []*listener.Filter{{Name: "network-filter"}},
				},
			},
		},
	}

	gatewayOut := []*xdsapi.Listener{
		{
			Name: "80",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 80,
						},
					},
				},
			},
			ListenerFilters: []*listener.ListenerFilter{{Name: "foo"}},
			FilterChains: []*listener.FilterChain{
				{
					FilterChainMatch: &listener.FilterChainMatch{
						ServerNames: []string{"match.com", "*.foo.com", "foo.com"},
					},
					Filters: []*listener.Filter{
						{
							Name: xdsutil.HTTPConnectionManager,
							ConfigType: &listener.Filter_TypedConfig{
								TypedConfig: util.MessageToAny(&http_conn.HttpConnectionManager{
									HttpFilters: []*http_conn.HttpFilter{
										{Name: "http-filter1"},
										{Name: "http-filter2"},
										{Name: "http-filter3"},
									},
								}),
							},
						},
					},
				},
			},
		},
		{
			Name: "another-listener",
			Address: &core.Address{
				Address: &core.Address_SocketAddress{
					SocketAddress: &core.SocketAddress{
						PortSpecifier: &core.SocketAddress_PortValue{
							PortValue: 443,
						},
					},
				},
			},
			FilterChains: []*listener.FilterChain{
				{
					FilterChainMatch: &listener.FilterChainMatch{
						ServerNames: []string{"nomatch.com", "*.foo.com"},
					},
					Filters: []*listener.Filter{{Name: "network-filter"}},
				},
			},
		},
	}

	sidecarProxy := &model.Proxy{Type: model.SidecarProxy, ConfigNamespace: "not-default",
		Metadata: map[string]string{"foo": "sidecar", "bar": "proxy", "ISTIO_VERSION": "1.2.2"}}
	gatewayProxy := &model.Proxy{Type: model.Router, ConfigNamespace: "not-default"}
	serviceDiscovery := &fakes.ServiceDiscovery{}
	env := newTestEnvironment(serviceDiscovery, testMesh, buildEnvoyFilterConfigStore(configPatches))
	push := model.NewPushContext()
	push.InitContext(env)

	type args struct {
		patchContext networking.EnvoyFilter_PatchContext
		proxy        *model.Proxy
		push         *model.PushContext
		listeners    []*xdsapi.Listener
		skipAdds     bool
	}
	tests := []struct {
		name string
		args args
		want []*xdsapi.Listener
	}{
		{
			name: "sidecar inbound lds",
			args: args{
				patchContext: networking.EnvoyFilter_SIDECAR_INBOUND,
				proxy:        sidecarProxy,
				push:         push,
				listeners:    sidecarInboundIn,
				skipAdds:     false,
			},
			want: sidecarInboundOut,
		},
		{
			name: "gateway lds",
			args: args{
				patchContext: networking.EnvoyFilter_GATEWAY,
				proxy:        gatewayProxy,
				push:         push,
				listeners:    gatewayIn,
				skipAdds:     false,
			},
			want: gatewayOut,
		},
		{
			name: "sidecar outbound lds",
			args: args{
				patchContext: networking.EnvoyFilter_SIDECAR_OUTBOUND,
				proxy:        sidecarProxy,
				push:         push,
				listeners:    sidecarOutboundIn,
				skipAdds:     false,
			},
			want: sidecarOutboundOut,
		},
		{
			name: "sidecar outbound lds - skip adds",
			args: args{
				patchContext: networking.EnvoyFilter_SIDECAR_OUTBOUND,
				proxy:        sidecarProxy,
				push:         push,
				listeners:    sidecarOutboundInNoAdd,
				skipAdds:     true,
			},
			want: sidecarOutboundOutNoAdd,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyListenerPatches(tt.args.patchContext, tt.args.proxy, tt.args.push,
				tt.args.listeners, tt.args.skipAdds)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("ApplyListenerPatches(): %s mismatch (-want +got):\n%s", tt.name, diff)
			}
		})
	}
}
