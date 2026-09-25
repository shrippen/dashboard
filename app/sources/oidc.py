"""OpenID Connect provider adapter (authentik): discovery, code exchange, ID token check."""

import time
from dataclasses import dataclass

from authlib.jose import JsonWebKey, JsonWebToken
from authlib.jose.errors import JoseError

from app.drivers import http
from app.sources.base import SourceError

DISCOVERY = ".well-known/openid-configuration"
CACHE_S = 3600
ALGORITHMS = ["RS256", "ES256", "HS256"]
OK = 200

_cache: dict[str, tuple[float, dict]] = {}


@dataclass(frozen=True)
class Provider:
    issuer: str
    authorize: str
    token: str
    jwks_uri: str
    end_session: str | None


def discover(issuer: str) -> Provider:
    base = issuer if issuer.endswith("/") else issuer + "/"
    now = time.monotonic()
    cached = _cache.get(base)
    if cached and now - cached[0] < CACHE_S:
        meta = cached[1]
    else:
        try:
            meta = http.get_json(base + DISCOVERY)
        except (http.HttpError, http.EgressDenied) as exc:
            raise SourceError(f"discovery: {exc}") from exc
        _cache[base] = (now, meta)

    return Provider(
        issuer=meta["issuer"],
        authorize=meta["authorization_endpoint"],
        token=meta["token_endpoint"],
        jwks_uri=meta["jwks_uri"],
        end_session=meta.get("end_session_endpoint"),
    )


def exchange(provider: Provider, code: str, redirect_uri: str, client_id: str,
             secret: str, verifier: str) -> dict:
    form = {
        "grant_type": "authorization_code",
        "code": code,
        "redirect_uri": redirect_uri,
        "client_id": client_id,
        "client_secret": secret,
        "code_verifier": verifier,
    }
    try:
        response = http.request("POST", provider.token, data=form)
    except (http.HttpError, http.EgressDenied) as exc:
        raise SourceError(f"token: {exc}") from exc
    if response.status_code != OK:
        raise SourceError(f"token: HTTP {response.status_code}")
    return response.json()


def claims(provider: Provider, id_token: str, client_id: str, nonce: str) -> dict:
    try:
        keys = JsonWebKey.import_key_set(http.get_json(provider.jwks_uri))
        options = {
            "iss": {"essential": True, "value": provider.issuer},
            "aud": {"essential": True, "value": client_id},
            "nonce": {"essential": True, "value": nonce},
            "exp": {"essential": True},
        }
        decoded = JsonWebToken(ALGORITHMS).decode(id_token, keys, claims_options=options)
        decoded.validate()
    except (JoseError, http.HttpError, ValueError) as exc:
        raise SourceError(f"id_token: {exc}") from exc
    return dict(decoded)
