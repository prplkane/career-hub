from __future__ import annotations

import re
from pathlib import Path
import unittest


PROJECT_ROOT = Path(__file__).resolve().parents[1]
ATLAS_PATH = PROJECT_ROOT / "docs" / "reference" / "architecture_atlas.md"
SQLITE_OWNER_PATH = PROJECT_ROOT / "backend/internal/applications/schema/steps_generated.go"


class ArchitectureAtlasTests(unittest.TestCase):
    def setUp(self) -> None:
        self.atlas = ATLAS_PATH.read_text(encoding="utf-8")

    def test_sqlite_table_index_matches_current_schema_owner(self) -> None:
        source = SQLITE_OWNER_PATH.read_text(encoding="utf-8")
        table_names = set(
            re.findall(r"CREATE TABLE IF NOT EXISTS\s+([a-z_]+)", source)
        )
        self.assertTrue(table_names)

        index = self.atlas.split("### 6.4 Complete table index", 1)[1].split(
            "## 7. Stored non-SQL model UML", 1
        )[0]
        documented_names = set(
            re.findall(r"^\| [^|]+ \| `([a-z_]+)` \|", index, re.MULTILINE)
        )
        self.assertEqual(table_names, documented_names)

        version_source = SQLITE_OWNER_PATH.with_name("steps.go").read_text(encoding="utf-8")
        version_match = re.search(r"^const Version = (\d+)$", version_source, re.MULTILINE)
        self.assertIsNotNone(version_match)
        assert version_match is not None
        self.assertIn(
            f"schema version {version_match.group(1)}",
            self.atlas.casefold(),
        )

    def test_required_mermaid_diagram_families_are_present(self) -> None:
        diagrams = re.findall(r"```mermaid\n(.*?)\n```", self.atlas, re.DOTALL)
        self.assertEqual(self.atlas.count("```mermaid"), len(diagrams))
        self.assertGreaterEqual(len(diagrams), 20)
        first_lines = {diagram.splitlines()[0].strip() for diagram in diagrams}
        self.assertTrue(
            {"flowchart LR", "classDiagram", "erDiagram", "stateDiagram-v2", "sequenceDiagram"}
            <= first_lines
        )

        sequence_diagrams = [
            diagram
            for diagram in diagrams
            if diagram.splitlines()[0].strip() == "sequenceDiagram"
        ]
        for diagram in sequence_diagrams:
            self.assertNotIn(
                ";",
                diagram,
                "raw semicolons split Mermaid sequence messages into commands",
            )

    def test_primary_entry_documents_link_to_the_atlas(self) -> None:
        start_here = (PROJECT_ROOT / "START_HERE.md").read_text(encoding="utf-8")
        architecture = (PROJECT_ROOT / "docs" / "reference" / "architecture.md").read_text(
            encoding="utf-8"
        )
        self.assertIn("(docs/reference/architecture_atlas.md)", start_here)
        self.assertIn("(architecture_atlas.md)", architecture)

    def test_all_local_markdown_links_resolve(self) -> None:
        targets = re.findall(r"\[[^\]]+\]\(([^)]+)\)", self.atlas)
        missing: list[str] = []
        for target in targets:
            if target.startswith(("#", "http://", "https://")):
                continue
            relative_path = target.split("#", 1)[0]
            if not (ATLAS_PATH.parent / relative_path).resolve().exists():
                missing.append(target)
        self.assertEqual([], missing)


if __name__ == "__main__":
    unittest.main()
