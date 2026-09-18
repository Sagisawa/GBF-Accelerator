import os
import datetime
import ssl
from pathlib import Path
from cryptography import x509
from cryptography.x509.oid import NameOID
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa

from config_manager import get_base_dir

CERTS_DIR = get_base_dir() / "certs"
CA_CERT_PATH = CERTS_DIR / "ca.crt"
CA_KEY_PATH = CERTS_DIR / "ca.key"
SERVER_CERT_PATH = CERTS_DIR / "server.crt"
SERVER_KEY_PATH = CERTS_DIR / "server.key"

# Strictly scoped GBF domains for Server Certificate SAN (RFC 6125 compliant, no broad *.akamaized.net)
SAN_DOMAINS = [
    "*.granbluefantasy.jp",
    "granbluefantasy.jp",
    "*.granbluefantasy.com",
    "granbluefantasy.com",
    "*.mbga.jp",
    "mbga.jp",
    "*.game.mbga.jp",
    "gbf.game.mbga.jp",
    "game.mbga.jp",
    "*.sp.pf.mbga.jp",
    "g12016007.sp.pf.mbga.jp",
    "sp.pf.mbga.jp",
    "*.pf.mbga.jp",
    "pf.mbga.jp",
    "*.sp.mbga.jp",
    "sp.mbga.jp",
    "*.connect.mobage.jp",
    "connect.mobage.jp",
    "*.mobage.jp",
    "mobage.jp",
    "*.game.mobage.jp",
    "gbf.game.mobage.jp",
    "game.mobage.jp",
    "*.sp.pf.mobage.jp",
    "g12016007.sp.pf.mobage.jp",
    "sp.pf.mobage.jp",
    "*.pf.mobage.jp",
    "pf.mobage.jp",
    "prd-game-a-granbluefantasy.akamaized.net",
    "prd-game-a1-granbluefantasy.akamaized.net",
    "prd-game-a2-granbluefantasy.akamaized.net",
    "prd-game-a3-granbluefantasy.akamaized.net",
    "prd-game-a4-granbluefantasy.akamaized.net",
    "prd-game-a5-granbluefantasy.akamaized.net",
    "prd-game-a-granbluefantasy-steam.akamaized.net",
    "prd-game-a1-granbluefantasy-steam.akamaized.net",
    "prd-game-a2-granbluefantasy-steam.akamaized.net",
    "prd-game-a3-granbluefantasy-steam.akamaized.net",
    "prd-game-a4-granbluefantasy-steam.akamaized.net",
    "prd-game-a5-granbluefantasy-steam.akamaized.net",
    "gbf.akamaized.net",
    "*.gbf.akamaized.net",
    "granbluefantasy.akamaized.net",
    "*.granbluefantasy.akamaized.net",
]

# Serial numbers / signatures of the previously embedded public CA to trigger automatic replacement
OLD_LEAKED_SERIALS = {
    0x370C2B333DF5E484751DA4E24C806E4AAE24808B,
}

def generate_ca():
    """Dynamically generate a unique, per-machine 2048-bit RSA Root CA key and self-signed certificate."""
    print("[*] Generating unique local Root CA certificate and private key...")
    CERTS_DIR.mkdir(parents=True, exist_ok=True)
    ca_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    subject = issuer = x509.Name([
        x509.NameAttribute(NameOID.COMMON_NAME, "GBF Local Accelerator Root CA"),
        x509.NameAttribute(NameOID.ORGANIZATION_NAME, "GBF Local Accelerator"),
    ])
    now = datetime.datetime.now(datetime.timezone.utc)
    ca_cert = (
        x509.CertificateBuilder()
        .subject_name(subject)
        .issuer_name(issuer)
        .public_key(ca_key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - datetime.timedelta(days=1))
        .not_valid_after(now + datetime.timedelta(days=3650))
        .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
        .add_extension(
            x509.KeyUsage(
                digital_signature=True,
                key_encipherment=False,
                key_cert_sign=True,
                key_agreement=False,
                crl_sign=True,
                encipher_only=False,
                decipher_only=False,
                content_commitment=False,
                data_encipherment=False,
            ),
            critical=True,
        )
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(ca_key.public_key()),
            critical=False,
        )
        .sign(ca_key, hashes.SHA256())
    )

    with open(CA_KEY_PATH, "wb") as f:
        f.write(ca_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.TraditionalOpenSSL,
            encryption_algorithm=serialization.NoEncryption(),
        ))
    with open(CA_CERT_PATH, "wb") as f:
        f.write(ca_cert.public_bytes(serialization.Encoding.PEM))

    # Invalidate existing server cert if CA was regenerated
    if SERVER_CERT_PATH.exists():
        SERVER_CERT_PATH.unlink(missing_ok=True)
    if SERVER_KEY_PATH.exists():
        SERVER_KEY_PATH.unlink(missing_ok=True)
    print(f"[+] Local Root CA successfully generated: {CA_CERT_PATH}")

def ensure_ca():
    CERTS_DIR.mkdir(parents=True, exist_ok=True)
    need_generate = False
    if not (CA_CERT_PATH.exists() and CA_KEY_PATH.exists()):
        need_generate = True
    else:
        try:
            cert_data = CA_CERT_PATH.read_bytes()
            existing_cert = x509.load_pem_x509_certificate(cert_data)
            # Detect old hardcoded CA and regenerate unique one
            common_names = existing_cert.subject.get_attributes_for_oid(NameOID.COMMON_NAME)
            cn_val = common_names[0].value if common_names else ""
            if existing_cert.serial_number in OLD_LEAKED_SERIALS or cn_val == "GBF Speed CA":
                print("[!] Detected old public embedded CA cert. Regenerating secure unique local CA...")
                need_generate = True
        except Exception:
            need_generate = True

    if need_generate:
        generate_ca()

    with open(CA_KEY_PATH, "rb") as f:
        ca_key = serialization.load_pem_private_key(f.read(), password=None)
    with open(CA_CERT_PATH, "rb") as f:
        ca_cert = x509.load_pem_x509_certificate(f.read())
    return ca_cert, ca_key

def ensure_server_cert():
    ca_cert, ca_key = ensure_ca()
    need_generate = False
    if not (SERVER_CERT_PATH.exists() and SERVER_KEY_PATH.exists()):
        need_generate = True
    else:
        try:
            srv_data = SERVER_CERT_PATH.read_bytes()
            srv_cert = x509.load_pem_x509_certificate(srv_data)
            if srv_cert.issuer != ca_cert.subject:
                need_generate = True
            else:
                ext = srv_cert.extensions.get_extension_for_oid(x509.ExtensionOID.SUBJECT_ALTERNATIVE_NAME)
                current_sans = set(ext.value.get_values_for_type(x509.DNSName))
                if not set(SAN_DOMAINS).issubset(current_sans):
                    need_generate = True

                # Apple / Safari TLS compliance: ServerAuth EKU & validity <= 398 days
                try:
                    ext_eku = srv_cert.extensions.get_extension_for_oid(x509.ExtensionOID.EXTENDED_KEY_USAGE)
                    if x509.ExtendedKeyUsageOID.SERVER_AUTH not in ext_eku.value:
                        need_generate = True
                except Exception:
                    need_generate = True

                try:
                    srv_cert.extensions.get_extension_for_oid(x509.ExtensionOID.AUTHORITY_KEY_IDENTIFIER)
                except Exception:
                    need_generate = True

                if hasattr(srv_cert, "not_valid_before_utc"):
                    not_before = srv_cert.not_valid_before_utc
                    not_after = srv_cert.not_valid_after_utc
                else:
                    not_before = srv_cert.not_valid_before
                    not_after = srv_cert.not_valid_after
                if not_before and not_after and (not_after - not_before).days > 398:
                    need_generate = True
        except Exception:
            need_generate = True

    if not need_generate:
        return

    print("[*] Generating scoped server certificate for GBF domains (Apple TLS compliant)...")
    server_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    server_name = x509.Name([
        x509.NameAttribute(NameOID.COMMON_NAME, "*.granbluefantasy.jp"),
        x509.NameAttribute(NameOID.ORGANIZATION_NAME, "GBF Local Accelerator"),
    ])
    sans = [x509.DNSName(d) for d in SAN_DOMAINS]

    now = datetime.datetime.now(datetime.timezone.utc)
    server_cert = (
        x509.CertificateBuilder()
        .subject_name(server_name)
        .issuer_name(ca_cert.subject)
        .public_key(server_key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - datetime.timedelta(days=1))
        .not_valid_after(now + datetime.timedelta(days=365))  # Apple/Safari TLS policy recommendations (<= 398 days)
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(x509.SubjectAlternativeName(sans), critical=False)
        .add_extension(
            x509.ExtendedKeyUsage([x509.ExtendedKeyUsageOID.SERVER_AUTH]),
            critical=False,
        )
        .add_extension(
            x509.AuthorityKeyIdentifier.from_issuer_public_key(ca_key.public_key()),
            critical=False,
        )
        .add_extension(
            x509.SubjectKeyIdentifier.from_public_key(server_key.public_key()),
            critical=False,
        )
        .sign(ca_key, hashes.SHA256())
    )

    with open(SERVER_KEY_PATH, "wb") as f:
        f.write(server_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.TraditionalOpenSSL,
            encryption_algorithm=serialization.NoEncryption(),
        ))
    with open(SERVER_CERT_PATH, "wb") as f:
        f.write(server_cert.public_bytes(serialization.Encoding.PEM))
        f.write(ca_cert.public_bytes(serialization.Encoding.PEM))
    print(f"[+] Scoped Server cert generated with full chain: {SERVER_CERT_PATH}")

def get_server_ssl_context() -> ssl.SSLContext:
    ensure_server_cert()
    ctx = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
    ctx.load_cert_chain(str(SERVER_CERT_PATH), str(SERVER_KEY_PATH))
    return ctx

def get_ca_fingerprint_sha256() -> str:
    """Calculate and return formatted SHA-256 fingerprint of Root CA certificate."""
    ensure_ca()
    if CA_CERT_PATH.is_file():
        try:
            cert_data = CA_CERT_PATH.read_bytes()
            cert = x509.load_pem_x509_certificate(cert_data)
            raw_hex = cert.fingerprint(hashes.SHA256()).hex().upper()
            return ":".join(raw_hex[i:i+2] for i in range(0, len(raw_hex), 2))
        except Exception:
            pass
    return "未知 / 证书未生成"

if __name__ == "__main__":
    ensure_ca()
    ensure_server_cert()
    print("CA Fingerprint (SHA-256):", get_ca_fingerprint_sha256())
    print("Done!")
