# Remote domain DNS bundle

The fake-IP service resolver selects the leaf proxy for the queried hostname
(using the existing HTTPS/TCP port 443 rule matching). Before returning the
first fake A or AAAA answer, it requests one complete domain bundle from that
node. The reserved destination remains `server-dns.invalid:53`.

Wire format is DNS over TCP with one HTTPS question. Experimental EDNS option
65001 with the one-byte value 1 requests this extension. A supporting Xray
returns HTTPS records in Answer, its configured resolver's real address results
in Additional under the original question name, and the same option as an
acknowledgement. This does not use ANY or a multiple-question message. Ordinary
queries keep their existing semantics.

Mihomo stores the unmodified HTTPS records and real addresses in one bounded
cache keyed by leaf node name and normalized primary hostname. The lifetime is
the minimum TTL of the complete result. Expired bundles are never served stale;
an address or HTTPS query refreshes the whole entry. Concurrent questions for
the same node/domain share one request. Fake-IP responses are synthesized from
copies, so rewriting hints cannot corrupt the real addresses or ECH parameters.
Fake-IP is a stable hostname index, not a second real-address cache. Its address
allocation may outlive DNS data so existing application connections keep their
hostname association; DNS metadata still expires independently of that index.
The synthesized response TTL does not exceed the bundle's remaining lifetime.

The matching Xray change resolves addresses through LookupIP, including hosts,
address-family policy and expected-IP filters. It makes the complete result
available to later address and HTTPS lookups. Freedom also consults this cache
in AsIs mode, before its normal final-destination rules. Domain routing still
sees the FQDN. Internal record lookups use a fresh DNS session so a user's raw
socket/splice state cannot bypass the DNS tunnel framing.

A domain whose rule selects DIRECT has no proxy server to ask. Its address
query fails as before, so fake-IP still allocates locally, and its HTTPS query
goes to `direct-nameserver`. It never reaches the public resolver: that
resolver follows routing rules, so asking it would both answer with another
network's view of a domain a direct connection is built on, and carry every
direct domain's name out through a proxy. Without `direct-nameserver`
configured the query falls through to the ordinary resolution path, which is
local as well.

A leaf with no server that is not DIRECT either -- reject, an empty group, the
pass-through types -- is one nothing ever connects through, so its HTTPS query
is answered here with NODATA. Whatever the records said would be discarded
along with the connection, and a rejected domain is queried as often as any
other, so spending a proxied public query on one is pure cost.

Older nodes fall back to ordinary HTTPS queries; lack of bundle support is
remembered for five minutes. Failed address warmups still permit local fake-IP
allocation and do not trigger public A/AAAA resolution. Incomplete results and
zero TTL results are not cached. HTTPS NODATA currently has no preserved SOA
negative-cache TTL, so its bundle is returned with zero TTL rather than an
invented lifetime. Bundles are volatile and rebuilt after configuration reload.

ALPN, ECH and port are available to applications that query HTTPS records.
Caching cannot add ECH to an application handshake that has already been sent.
As before, DNS cannot know the application's future port, network, or source
routing metadata: deployments whose actual flow selects a different node from
the DNS port-443 selection cannot assume both exits have identical DNS data.

Validation:

- `go test -race ./dns ./tunnel` (using patched dependency GOFLAGS).
- Set `XRAY_BUNDLE_TEST_BINARY` to the matching Xray binary to enable
  `TestDomainBundleXrayIntegration`. It starts a local DNS server, Xray SOCKS
  inbound and echo destination, verifies A-first HTTPS cache warming, then
  verifies the first AsIs FQDN connection adds no DNS queries. No public network
  is involved in that test.
