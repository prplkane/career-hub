export function initHelpCenter({ onNavigate } = {}) {
  const panel = document.querySelector("#help-center-panel");
  const trigger = document.querySelector("#open-help-center");
  if (!panel || !trigger) return;

  panel.addEventListener("toggle", () => {
    trigger.setAttribute("aria-expanded", String(panel.matches(":popover-open")));
  });

  panel.addEventListener("click", (event) => {
    const action = event.target.closest("[data-help-destination]");
    if (!action || !panel.contains(action)) return;
    const destination = action.dataset.helpDestination;
    panel.hidePopover();
    trigger.focus();
    if (destination && typeof onNavigate === "function") {
      void onNavigate(destination);
    }
  });
}
