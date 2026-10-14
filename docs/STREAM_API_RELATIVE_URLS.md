# fnOS stream API relative URL handling

`stream_api_relative_base_url` is an opt-in compatibility capability. It is
disabled when omitted and must remain disabled in existing production
configurations unless the topology is separately verified.

## Trust boundary

The base comes only from local configuration. It is never inferred from the
client `Host`, forwarded headers, the public proxy origin, or response JSON.
It must be a plain HTTP(S) origin with no credentials, path, query, or
fragment, and its exact host/port must already be authorized by
`allowed_upstreams`.

Only normalized root-relative paths below `/v/api/v1/media/range/` are
resolved. Network-path references (`//host/...`), dot segments, encoded paths,
backslashes, fragments, other path prefixes, and absolute URLs outside the
existing allowlist remain unchanged. The resolved URL is checked against both
the configured base origin and `allowed_upstreams`; the media gateway checks
the allowlist again when a ticket is redeemed and on every redirect hop.

## Topology warning

An fnOS response such as `/v/api/v1/media/range/...` names an fnOS media
endpoint. It is not the CloudDrive2 URL stored in a `.strm` file. Resolving it
against an fnOS base makes the media gateway fetch through fnOS; it does not
create a CloudDrive2 direct path. Resolving it against the public TV origin
would route back through the reverse proxy and is not a direct-media design.

Before enabling the option, independently verify that the media node can reach
the chosen private origin, that authentication and byte ranges work, that the
origin is intentionally allowlisted, and that this extra hop matches the
desired topology. This option provides compatible parsing and ticket issuance;
it does not establish a performance improvement.
