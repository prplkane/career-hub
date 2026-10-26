import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import vm from "node:vm";

const source = readFileSync(new URL("../src/intake_ui_static/features/application_materials.js", import.meta.url), "utf8");
const connectionFile = String.raw`C:\Users\Example\AppData\Local\CareerHub\dev\session\agent-connection.json`;

function loadHelper(windowStub) {
  const start = source.indexOf("async function buildAgentMaterialCommand(");
  const end = source.indexOf("\nasync function copyAgentMaterialCommand(", start);
  assert.ok(start > 0 && end > start, "buildAgentMaterialCommand is defined");
  const context = { window: windowStub };
  vm.runInNewContext(`${source.slice(start, end)}\nglobalThis.helper = buildAgentMaterialCommand;`, context);
  return context.helper;
}

test("the retired Python agent command is never offered", () => {
  assert.equal(source.includes("python -m src.agent_workflow"), false);
});

test("desktop connection yields a complete, runnable agent command", async () => {
  const helper = loadHelper({
    careerHubDesktop: {
      agent: {
        getConnection: async () => ({
          available: true,
          executablePath: String.raw`C:\Program Files\Career Hub\career-hub-agent.exe`,
          server: "http://127.0.0.1:51234",
          connection: connectionFile,
        }),
      },
    },
  });
  const result = await helper("job_abc", "cover_letter");
  assert.equal(result.complete, true);
  assert.equal(
    result.command,
    String.raw`& "C:\Program Files\Career Hub\career-hub-agent.exe" material-context --connection "` + connectionFile + `" --job-id job_abc --kind cover_letter --pretty`,
  );
  assert.equal(result.command.includes("--server"), false, "the token-free connection file replaces --server");
});

test("plain browser mode falls back to the skill placeholders", async () => {
  const helper = loadHelper({});
  const result = await helper("job_abc", "resume");
  assert.equal(result.complete, false);
  assert.equal(result.command, "& $agent material-context --connection $connection --job-id job_abc --kind resume --pretty");
});

test("an unavailable, failing or file-less desktop connection also falls back", async () => {
  const serverWithoutFile = async () => ({ available: true, executablePath: String.raw`C:\agent.exe`, server: "http://127.0.0.1:51234" });
  for (const getConnection of [async () => ({ available: false }), async () => { throw new Error("ipc"); }, serverWithoutFile]) {
    const helper = loadHelper({ careerHubDesktop: { agent: { getConnection } } });
    const result = await helper("job_abc", "resume");
    assert.equal(result.complete, false);
    assert.match(result.command, /^& \$agent material-context --connection \$connection /);
  }
});
