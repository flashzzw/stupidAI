import copy
from pathlib import Path
import unittest

from codex_skill_selection_probe import selection_overrides


class SelectionOverridesTests(unittest.TestCase):
    def test_selected_entry_replaced_without_mutating_other_choices(self):
        config = {"skills": {"config": [{"path": "/shared/SKILL.md", "enabled": True},
                    {"path": "/other/SKILL.md", "enabled": False, "future_field": "preserve"}]}}
        before = copy.deepcopy(config)
        override = selection_overrides(config, Path("/shared/SKILL.md"), False)
        self.assertEqual(override, {"skills.config": [{"path": "/other/SKILL.md", "enabled": False,
                          "future_field": "preserve"}, {"path": "/shared/SKILL.md", "enabled": False}]})
        self.assertEqual(config, before)

    def test_new_choice_does_not_clear_an_existing_disable(self):
        config = {"skills": {"config": [{"path": "/other/SKILL.md", "enabled": False}]}}
        result = selection_overrides(config, "/new/SKILL.md", True)
        self.assertEqual(len(result["skills.config"]), 2)
        self.assertFalse(result["skills.config"][0]["enabled"])

    def test_ambiguous_native_config_rejected(self):
        for entries in ({}, [{"path": "relative", "enabled": False}], [{"path": "/one", "enabled": "false"}],
                        [{"path": "/one", "enabled": False}] * 2, [{}]):
            with self.subTest(entries=entries), self.assertRaises(ValueError):
                selection_overrides({"skills": {"config": entries}}, "/selected", False)

    def test_invalid_selected_state_rejected(self):
        for path, state in [("relative", False), ("/selected", "false")]:
            with self.subTest(path=path, state=state), self.assertRaises(ValueError):
                selection_overrides({}, path, state)


if __name__ == "__main__":
    unittest.main()
