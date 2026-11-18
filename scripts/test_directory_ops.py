import json,pathlib,tempfile,unittest
from directory_ops import init
from ops import read_env
from release import seal

class DirectoryOperationsTests(unittest.TestCase):
 def setUp(self):
  self.tmp=tempfile.TemporaryDirectory();self.addCleanup(self.tmp.cleanup);self.base=pathlib.Path(self.tmp.name);self.bundle=self.base/'bundle';self.bundle.mkdir();self.root=self.base/'runtime'
  for name in ['images.tar','compose.json','Caddyfile','directory.compose.json','Directory.Caddyfile']:(self.bundle/name).write_text('{}')
  seal(self.bundle,{'format':1,'kind':'bundle','version':'0.4.0','images':{name:'sha256:'+'1'*64 for name in ['gateway','openim','mongo','redis','caddy']}})
 def test_private_token_and_immutable_images(self):
  init(self.root,self.bundle,'config.example.com');env=read_env(self.root/'.env')
  self.assertGreaterEqual(len(env['DIRECTORY_TOKEN']),32);self.assertEqual((self.root/'.env').stat().st_mode&0o777,0o600);self.assertEqual(env['GATEWAY_IMAGE'],'sha256:'+'1'*64)
  before=(self.root/'.env').read_bytes()
  with self.assertRaises(ValueError):init(self.root,self.bundle,'new.example.com')
  self.assertEqual(before,(self.root/'.env').read_bytes())
 def test_bad_domain_and_tampered_bundle_do_not_initialize(self):
  for domain in ['https://config.example.com','config.example.com/path','a\nTOKEN=evil','localhost']:
   with self.assertRaises(ValueError):init(self.root,self.bundle,domain)
  (self.bundle/'images.tar').write_text('tampered')
  with self.assertRaises(ValueError):init(self.root,self.bundle,'config.example.com')
  self.assertFalse(self.root.exists())
