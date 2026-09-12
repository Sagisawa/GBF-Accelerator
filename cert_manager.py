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

SAN_DOMAINS = [
    "*.granbluefantasy.jp",
    "granbluefantasy.jp",
    "*.granbluefantasy.com",
    "granbluefantasy.com",
    "*.akamaized.net",
    "*.gbf.akamaized.net",
    "*.mbga.jp",
    "mbga.jp",
]

EMBEDDED_CA_CERT = b"""-----BEGIN CERTIFICATE-----
MIIDHzCCAgegAwIBAgIUNwwrMz315IR1HaTiTIBuSq4kgIswDQYJKoZIhvcNAQEL
BQAwNzEVMBMGA1UEAwwMR0JGIFNwZWVkIENBMR4wHAYDVQQKDBVHQkYgTG9jYWwg
QWNjZWxlcmF0b3IwHhcNMjYwOTExMDczMzQ3WhcNMzYwOTA5MDczMzQ3WjA3MRUw
EwYDVQQDDAxHQkYgU3BlZWQgQ0ExHjAcBgNVBAoMFUdCRiBMb2NhbCBBY2NlbGVy
YXRvcjCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBALtW+91OnJkDHhR4
AtoODHnQVHUQEicDa08WFjqNBwQi/qg4wGPluo2ZmgHeL32P3UJwS9HNoMqap4Qg
6918b1i4BU0Ya5Tlqoc3mr+BtU4Ck3W7wKuDFWRqbKnHpzIbdlTAOjWXj1DBlCjx
OAr/4oUjsQ4PA3tqe+OHXJI8yZjS2R4WeF2hOrUhm6keWspFBPquydFs45OVd8JE
qkbLVIE9/3mbQqUu+u8FnAymQtJKMrfb8ZRYEPyK0z9bf7DoFmvv4S+uLYaXwvwS
LXr/boTJgYtTEgqyhxQGhxrcuId23z4xsHElNCiL6h9C/n3FptWYGzu5E9SYFY2T
M4MV7y0CAwEAAaMjMCEwDwYDVR0TAQH/BAUwAwEB/zAOBgNVHQ8BAf8EBAMCAYYw
DQYJKoZIhvcNAQELBQADggEBABeQ6ZxB4gU1OHmGaZkucl/cnYdkoT0Q85LV3iDd
raLwtx2KiyRq5c4whlWfERue8m188u/Mhfvb6+Zb/PD9UD9NHx0PbZwrSiJqCnAV
SEX5fc7UJ3UdOvxcLo3OZPo9n0dFnzdoTUHsWAQLPhUg6jPEcs44sPr+c/SJ6bMk
NeUWqNuiLF8QNDp0reVjcPQsDK3QLyAePBkc+3FtqjyKDVv1xkbZ15KZMPUaah0T
TEci4wyKtKaaaXMLUaRLINa9GLACSABCayKVLLhbTWRS/zmwkoxIj3ve1agflAVm
UujVRZIDjFC0rvUJC2Qh1sTsPd3yXFt32WlfRqQiXqv0kE8=
-----END CERTIFICATE-----
"""

EMBEDDED_CA_KEY = b"""-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEAu1b73U6cmQMeFHgC2g4MedBUdRASJwNrTxYWOo0HBCL+qDjA
Y+W6jZmaAd4vfY/dQnBL0c2gypqnhCDr3XxvWLgFTRhrlOWqhzeav4G1TgKTdbvA
q4MVZGpsqcenMht2VMA6NZePUMGUKPE4Cv/ihSOxDg8De2p744dckjzJmNLZHhZ4
XaE6tSGbqR5aykUE+q7J0Wzjk5V3wkSqRstUgT3/eZtCpS767wWcDKZC0koyt9vx
lFgQ/IrTP1t/sOgWa+/hL64thpfC/BItev9uhMmBi1MSCrKHFAaHGty4h3bfPjGw
cSU0KIvqH0L+fcWm1ZgbO7kT1JgVjZMzgxXvLQIDAQABAoIBAB4x1+iEmiLjaL69
1R/WMdaUaHhxvatCFtKpaa3IO0BEb60ncILpbRcTkcoJSLhBLtVdiirnrKnbIXLf
Z4TMYJn5FwmlDPnzxneC09NYEaPgMGpCd7xtJU6JBLicsGsYGAty7C7lHblTahDr
SDAlrBnvdcMhUlta/1rd32LGn2udEZu6NHJJJP1gaPUB31N/E+S2lFdJWc+fT3k4
I/xHhhqGc/FLA7w7m3alttu7MY/muAqBKWDrtyLKP2wiXXpXi2oWiuH2byJMWo4f
79YSX068n731ozY339mrMNBE1AL8hcWnsxnrT3fBmdgLShSh2aOyxVnwIchVs6iX
rKFI+UECgYEA6S2OyWUnAJmEoPD+m0pXAiO9tiiZtnZOm6Pwi0ZYYNLr1QHY6tAs
grEgbycOwH16uMR+1V+eDT9VXYT0WFPhv/A87WIogcJk1qWWk4xXwJwjZFpEzaww
eOFdqIw+mZMCjVApSmbE6Wndk6tWZX2xMs7LKUSrdcnlrR3FW8H9MhECgYEAzazq
2eE+195cPPgV8uT1OU1L7zyYsYm6VwEJhObqqolx+c8a5ilRGN5nI2jye3Izi9r0
H+l5FhK6DB/oEm5UES2vlZZIHEABp+jH5U3348ggXud93hlD11b2p1yUd+dg45TD
Ctn3wTTgRcmRwoSHXx9K8c1t1tas+rcfBsWvz10CgYEAx+LZ6CLiEE2JuD1exNgx
RhBFbIXZXuSD9j/O0FV5JWcp6usue/wAa/hTCXW925y1OvaWk2roHgsQrp5up9kg
SF00nXnrp3Bw6OAB+HHyN5ahcEFBgd39n2Hx2659a0Duix0QiEsYuc6atx/FbDMX
V6qV1cacBNkSHhjLOiFNX0ECgYBrjmHCTuhuOvpBZ/sSamlS7fknwqiXL08i8Ifp
2FgfloDkAkou0qx2NNf6zIcBx1btbDL9/To1MNXaQVU7TjboRNvtfgl3vIEhLbpb
T8qyc5V6C9TmsI+prPCP1PpPOdCRMtpMcm/9uYkO9boj3upr9BFdIfCuyNTsx5aS
FA88gQKBgQCxT25+kLEqd89MFnt+zqWi8D47xvb9GZKPhVIOCdWtAmTgU+UDSOYA
56MRV8yVfQ9vdHJTlaFmrk+cH2eqnmwDPD8Q3ps9leWtO/Dh4ScoREFwlN4cR1YJ
jZxno6VFJONv4YTqbAoxezwg938QiXKMqzLjNXhgZevyZSWZopOipA==
-----END RSA PRIVATE KEY-----
"""

def ensure_ca():
    CERTS_DIR.mkdir(parents=True, exist_ok=True)
    if not (CA_CERT_PATH.exists() and CA_KEY_PATH.exists()):
        with open(CA_CERT_PATH, "wb") as f:
            f.write(EMBEDDED_CA_CERT)
        with open(CA_KEY_PATH, "wb") as f:
            f.write(EMBEDDED_CA_KEY)

    with open(CA_KEY_PATH, "rb") as f:
        ca_key = serialization.load_pem_private_key(f.read(), password=None)
    with open(CA_CERT_PATH, "rb") as f:
        ca_cert = x509.load_pem_x509_certificate(f.read())
    return ca_cert, ca_key

def ensure_server_cert():
    ca_cert, ca_key = ensure_ca()
    if SERVER_CERT_PATH.exists() and SERVER_KEY_PATH.exists():
        return

    print("[*] Generating wildcard server certificate for GBF domains...")
    server_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    server_name = x509.Name([
        x509.NameAttribute(NameOID.COMMON_NAME, "*.granbluefantasy.jp"),
        x509.NameAttribute(NameOID.ORGANIZATION_NAME, "GBF Local Accelerator"),
    ])
    sans = [x509.DNSName(d) for d in SAN_DOMAINS]

    server_cert = (
        x509.CertificateBuilder()
        .subject_name(server_name)
        .issuer_name(ca_cert.subject)
        .public_key(server_key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(days=1))
        .not_valid_after(datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=3650))
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(x509.SubjectAlternativeName(sans), critical=False)
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
    print(f"[+] Wildcard Server cert generated: {SERVER_CERT_PATH}")

def get_server_ssl_context() -> ssl.SSLContext:
    ensure_server_cert()
    ctx = ssl.create_default_context(ssl.Purpose.CLIENT_AUTH)
    ctx.load_cert_chain(str(SERVER_CERT_PATH), str(SERVER_KEY_PATH))
    return ctx

if __name__ == "__main__":
    ensure_ca()
    ensure_server_cert()
    print("Done!")
