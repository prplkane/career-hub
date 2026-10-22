"""Development-only compatibility entrypoint; product commands use career-hub-agent."""
from __future__ import annotations
import sys
from scripts.agent_workflow_audit import main

if __name__ == '__main__':
    if len(sys.argv) < 2 or sys.argv[1] != 'audit':
        raise SystemExit('Python product commands are retired. Use the verified career-hub-agent connection from Settings.')
    del sys.argv[1]
    raise SystemExit(main())
