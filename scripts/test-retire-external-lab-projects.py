import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('retire', Path(__file__).with_name('retire-external-lab-projects.py'))
retire = importlib.util.module_from_spec(spec)
spec.loader.exec_module(retire)


class OwnershipTests(unittest.TestCase):
    def container(self, directory='', mounts=None, name='/test'):
        return {'Name': name, 'Config': {'Labels': {'com.docker.compose.project.working_dir': directory}}, 'Mounts': mounts or []}

    def test_external_compose_projects(self):
        for path in ['/opt/oraculo/chat', '/opt/srl/core', '/opt/srl/srl-mcp/deploy']:
            self.assertTrue(retire.is_external(self.container(path)))

    def test_unlabelled_external_mock(self):
        self.assertTrue(retire.is_external(self.container(mounts=[{'Type': 'bind', 'Source': '/opt/srl/vllm/mock-nginx.conf'}])))

    def test_lab_and_similarly_named_paths_are_preserved(self):
        for path in ['/srv/ai-lab/observability', '/srv/ai-lab/open-webui', '/opt/srl-other', '/opt/oraculo-copy', '']:
            self.assertFalse(retire.is_external(self.container(path)))

    def test_name_alone_is_not_ownership(self):
        self.assertFalse(retire.is_external(self.container(name='/oraculo-api')))


if __name__ == '__main__':
    unittest.main()
