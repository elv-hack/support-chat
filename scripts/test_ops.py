import io,json,pathlib,tarfile,tempfile,unittest
from ops import Ops,atomic,safe_tar
from release import seal,verify

class FakeOps(Ops):
    def __init__(self,root):super().__init__(root);self.events=[];self.bad=set();self.backup_fail=False
    def stage(self,bundle):verify(bundle,'bundle');self.events.append('stage');return pathlib.Path(bundle)
    def command(self,args,capture=False):self.events.append('command:'+str(args[1]));return ''
    def up(self,release):
        self.events.append('up:'+pathlib.Path(release).name)
        if str(release) in self.bad:raise RuntimeError('not ready')
    def stop(self,release):self.events.append('stop:'+pathlib.Path(release).name)
    def backup(self,release,resume=True):
        self.events.append('backup')
        if self.backup_fail:raise RuntimeError('disk full')
        return self.root/'backup'

class OperationsTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup);self.base=pathlib.Path(self.temp.name)
        self.a=self.bundle('0.1.0');self.b=self.bundle('0.2.0');self.ops=FakeOps(self.base/'runtime');self.ops.init('test-chat',18090)
    def bundle(self,version,schema=1):
        p=self.base/version;p.mkdir();(p/'images.tar').write_bytes(b'fixture');(p/'compose.json').write_text('{}');(p/'Caddyfile').write_text('fixture')
        seal(p,{'format':1,'kind':'bundle','version':version,'schemaVersion':schema,'images':{k:'sha256:'+'1'*64 for k in ['gateway','openim','mongo','redis','caddy']}});return p
    def installed(self):self.ops.save({'current':str(self.a),'previous':None,'pending':None})
    def test_corruption_rejected_before_stop(self):
        self.installed();(self.b/'images.tar').write_bytes(b'tampered')
        with self.assertRaises(ValueError):self.ops.deploy(self.b)
        self.assertEqual(self.ops.events,[])
    def test_success_backups_before_switch_and_remembers_old(self):
        self.installed();self.ops.deploy(self.b)
        self.assertLess(self.ops.events.index('backup'),self.ops.events.index('up:0.2.0'))
        self.assertEqual(self.ops.state(),{'current':str(self.b),'previous':str(self.a),'pending':None})
    def test_failed_health_rolls_code_back_without_restoring_data(self):
        self.installed();self.ops.bad.add(str(self.b))
        with self.assertRaises(RuntimeError):self.ops.deploy(self.b)
        self.assertEqual(self.ops.state()['current'],str(self.a));self.assertIsNone(self.ops.state()['pending'])
        self.assertEqual(self.ops.events[-2:],['stop:0.2.0','up:0.1.0'])
    def test_failed_backup_never_starts_new_version(self):
        self.installed();self.ops.backup_fail=True
        with self.assertRaises(RuntimeError):self.ops.deploy(self.b)
        self.assertNotIn('up:0.2.0',self.ops.events);self.assertIn('up:0.1.0',self.ops.events)
    def test_failed_rollback_remains_recoverable(self):
        self.installed();self.ops.bad.update([str(self.a),str(self.b)])
        with self.assertRaises(RuntimeError):self.ops.deploy(self.b)
        self.assertIsNotNone(self.ops.state()['pending']);self.ops.bad.clear();self.ops.recover()
        self.assertEqual(self.ops.state()['current'],str(self.a));self.assertIsNone(self.ops.state()['pending'])
    def test_initial_failure_recovers_same_install_not_orphaned_volumes(self):
        self.ops.bad.add(str(self.b))
        with self.assertRaises(RuntimeError):self.ops.deploy(self.b)
        self.assertIsNotNone(self.ops.state()['pending']);self.ops.bad.clear();self.ops.recover()
        self.assertEqual(self.ops.state()['current'],str(self.b))
    def test_schema_change_refused_before_backup(self):
        self.installed();c=self.bundle('1.0.0',2)
        with self.assertRaises(ValueError):self.ops.deploy(c)
        self.assertNotIn('backup',self.ops.events)
    def test_interrupted_restore_cannot_boot_partial_data(self):
        self.ops.save({'current':None,'previous':None,'pending':{'target':str(self.b),'from':None,'restore':'backup'}})
        with self.assertRaises(ValueError):self.ops.recover()
        self.assertNotIn('up:0.2.0',self.ops.events)
    def test_exclusive_operation_lock(self):
        with self.ops.lock():
            with self.assertRaises(ValueError):
                with Ops(self.ops.root).lock():pass
    def test_tar_traversal_rejected(self):
        p=self.base/'bad.tar'
        with tarfile.open(p,'w') as archive:
            member=tarfile.TarInfo('../../escape');member.size=1;archive.addfile(member,io.BytesIO(b'x'))
        with self.assertRaises(ValueError):safe_tar(p)
    def test_plain_backup_error_resumes_old_services(self):
        class CopyFailure(Ops):
            resumed=False
            def stop(self,release):pass
            def compose(self,release,*args,capture=False):return 'a\nb\nc\nd'
            def command(self,args,capture=False):
                if args[1]=='inspect':return json.dumps({'Running':False,'OOMKilled':False,'ExitCode':0})
                raise RuntimeError('copy failed')
            def up(self,release):self.resumed=True
        ops=CopyFailure(self.ops.root)
        with self.assertRaises(RuntimeError):ops.backup(self.a)
        self.assertTrue(ops.resumed);self.assertEqual(list((ops.root/'backups').glob('*/backup.json')),[])
    def test_partial_stop_failure_attempts_to_resume_old_services(self):
        class StopFailure(Ops):
            resumed=False
            def stop(self,release):raise RuntimeError('partial stop')
            def up(self,release):self.resumed=True
        ops=StopFailure(self.ops.root)
        with self.assertRaises(RuntimeError):ops.backup(self.a)
        self.assertTrue(ops.resumed)

if __name__=='__main__':unittest.main()
