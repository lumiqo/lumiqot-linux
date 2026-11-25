function selectPrompt(prompt) {
  const range = document.createRange();
  range.selectNodeContents(prompt);
  const selection = window.getSelection();
  selection.removeAllRanges();
  selection.addRange(range);
}

function copyWithSelection(prompt) {
  selectPrompt(prompt);
  if (!document.execCommand("copy")) {
    throw new Error("Clipboard unavailable");
  }
  window.getSelection().removeAllRanges();
}

for (const button of document.querySelectorAll("[data-copy]")) {
  const prompt = document.getElementById(button.dataset.copy);
  const status = button.nextElementSibling;
  button.hidden = false;
  button.addEventListener("click", async () => {
    status.textContent = "";
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(prompt.textContent);
      } else {
        copyWithSelection(prompt);
      }
      status.textContent = "Copied";
    } catch {
      selectPrompt(prompt);
      status.textContent = "Prompt selected. Use Copy to copy it manually.";
    }
  });
}
