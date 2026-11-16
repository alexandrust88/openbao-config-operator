"""Verify delivered certificates against independent issuer identities, including wrong-CA controls."""
import base64
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

class PKITrustTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        os.environ.setdefault("LAB_CONTEXT", "kind-unit-test")
        os.environ.setdefault("LAB_NAMESPACE", "obco-lab")
        os.environ.setdefault("LAB_CONNECTION", "lab-openbao")
        from pki import check_tls
        cls.check_tls = staticmethod(check_tls)
        cls.temp = tempfile.TemporaryDirectory(prefix="obco-pki-test-")
        cls.addClassCleanup(cls.temp.cleanup)
        cls.directory = Path(cls.temp.name)
        cls.certificate("root")
        cls.certificate("intermediate", "root", ca=True)
        cls.certificate("other-intermediate", "root", ca=True)
        cls.certificate("leaf", "intermediate")
        cls.certificate("wrong-intermediate-leaf", "other-intermediate")
        cls.certificate("root-leaf", "root")
        cls.certificate("unrelated")
        cls.root = (cls.directory / "root.crt").read_bytes()
        cls.intermediate = (cls.directory / "intermediate.crt").read_bytes()

    @classmethod
    def certificate(cls, name, signer=None, ca=False):
        def run(*args):
            subprocess.run(["openssl", *args], cwd=cls.directory, check=True,
                           stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        run("ecparam", "-genkey", "-name", "prime256v1", "-noout", "-out", name + ".key")
        run("req", "-new", "-key", name + ".key", "-subj", "/CN=" + name, "-out", name + ".csr")
        extension = cls.directory / (name + ".ext")
        extension.write_text(
            "basicConstraints=critical,CA:" + ("TRUE" if ca or signer is None else "FALSE") + "\n"
            "keyUsage=critical,digitalSignature" + (",keyCertSign,cRLSign" if ca or signer is None else "") + "\n"
            "subjectKeyIdentifier=hash\nsubjectAltName=DNS:localhost,DNS:app.localhost\n"
            + ("authorityKeyIdentifier=keyid:always\n" if signer else ""))
        signing = ["-CA", signer + ".crt", "-CAkey", signer + ".key", "-CAcreateserial"] if signer else ["-signkey", name + ".key"]
        run("x509", "-req", "-in", name + ".csr", *signing, "-days", "2",
            "-extfile", str(extension), "-out", name + ".crt")

    def payload(self, name, ca):
        return {key: base64.b64encode((self.directory / filename).read_bytes()).decode()
                for key, filename in {"tls.crt": name + ".crt", "tls.key": name + ".key", "ca.crt": ca + ".crt"}.items()}

    def test_expected_chain(self):
        self.check_tls(self.payload("leaf", "intermediate"), self.root, self.intermediate)

    def test_unrelated_self_signed_output_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.check_tls(self.payload("unrelated", "unrelated"), self.root, self.intermediate)

    def test_other_intermediate_from_same_root_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.check_tls(self.payload("wrong-intermediate-leaf", "other-intermediate"), self.root, self.intermediate)

    def test_direct_root_signature_is_rejected(self):
        with self.assertRaises(subprocess.CalledProcessError):
            self.check_tls(self.payload("root-leaf", "root"), self.root, self.intermediate)


if __name__ == "__main__":
    unittest.main()
