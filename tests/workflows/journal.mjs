// Failure metadata contains route names, never request bodies or session headers.
export function requestSummary(request, status = null) {
  return { method: request.method(), path: new URL(request.url()).pathname, status };
}

export class StepJournal {
  constructor(scenario) {
    this.scenario = scenario;
    this.steps = [];
  }

  async step(name, action) {
    const entry = { name, status: "running" };
    this.steps.push(entry);
    try {
      const value = await action();
      entry.status = "passed";
      return value;
    } catch (cause) {
      entry.status = "failed";
      throw new Error(`[${this.scenario}] step "${name}" failed: ${cause.message}`, { cause });
    }
  }

  report(error = null) {
    return {
      scenario: this.scenario,
      result: error ? "FAIL" : "PASS",
      failed_step: this.steps.find((step) => step.status === "failed")?.name ?? null,
      reason: error?.message ?? null,
      steps: this.steps,
    };
  }
}
