"""Secrets at rest, password hashing and random tokens.

    master key (Docker secret)
        │ HKDF(purpose)
        ▼
    AES-256-GCM key ──► nonce(12) ‖ ciphertext‖tag   stored in *_enc columns
"""

import base64
import hashlib
import hmac
import os
import secrets
from enum import StrEnum

from argon2 import PasswordHasher
from argon2.exceptions import InvalidHashError, VerificationError
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF

from app.settings import DEV_MASTER_KEY_FILE, Settings

NONCE_LEN = 12
KEY_LEN = 32
TOKEN_BYTES = 32


class Purpose(StrEnum):
    CREDENTIAL = "credential"
    TOTP = "totp"
    NOTIFY = "notify"
    SETTING = "setting"


class MissingKeyError(RuntimeError):
    pass


_master: bytes | None = None
_hasher = PasswordHasher()


def init_crypto(settings: Settings) -> None:
    global _master
    _master = _load_master(settings)


def _load_master(settings: Settings) -> bytes:
    if settings.master_key:
        return master_from(settings.master_key)

    if not (settings.dashboard_dev or settings.dashboard_testing):
        raise MissingKeyError("master_key secret is required (see docker-compose.example.yml)")

    # Development only: key next to the database.
    path = settings.data_dir / DEV_MASTER_KEY_FILE
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.exists():
        path.write_text(base64.b64encode(os.urandom(KEY_LEN)).decode())

    return master_from(path.read_text().strip())


def _key(purpose: Purpose, master: bytes | None = None) -> bytes:
    master = master or _master
    if master is None:
        raise MissingKeyError("crypto not initialised")

    kdf = HKDF(algorithm=hashes.SHA256(), length=KEY_LEN, salt=None, info=purpose.encode())
    return kdf.derive(master)


def master_from(secret: str) -> bytes:
    return hashlib.sha256(secret.encode()).digest()


def encrypt(text: str, purpose: Purpose, master: bytes | None = None) -> bytes:
    nonce = os.urandom(NONCE_LEN)
    return nonce + AESGCM(_key(purpose, master)).encrypt(nonce, text.encode(), purpose.encode())


def decrypt(blob: bytes, purpose: Purpose) -> str:
    nonce, body = blob[:NONCE_LEN], blob[NONCE_LEN:]
    return AESGCM(_key(purpose)).decrypt(nonce, body, purpose.encode()).decode()


def hash_password(password: str) -> str:
    return _hasher.hash(password)


def check_password(stored: str | None, password: str) -> bool:
    if not stored:
        # Spend comparable time so missing accounts are not detectable.
        _hasher.hash(password)
        return False

    try:
        return _hasher.verify(stored, password)
    except (VerificationError, InvalidHashError):
        return False


def new_token() -> str:
    return secrets.token_urlsafe(TOKEN_BYTES)


def token_hash(token: str) -> str:
    """Tokens are stored hashed; a database leak does not leak sessions."""
    return hashlib.sha256(token.encode()).hexdigest()


def same(a: str, b: str) -> bool:
    return hmac.compare_digest(a.encode(), b.encode())
