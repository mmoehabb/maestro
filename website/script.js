// This is an illustrative demo. It does not launch agents or run commands.
const tabs = [...document.querySelectorAll('[role="tab"]')];

function selectTask(tab) {
  for (const item of tabs) {
    const selected = item === tab;
    item.setAttribute('aria-selected', String(selected));
    item.tabIndex = selected ? 0 : -1;
    document.getElementById(item.getAttribute('aria-controls')).hidden = !selected;
  }
}

for (const tab of tabs) {
  tab.disabled = false;
  tab.addEventListener('click', () => selectTask(tab));
  tab.addEventListener('keydown', (event) => {
    let index = tabs.indexOf(tab);
    if (event.key === 'ArrowRight') index = (index + 1) % tabs.length;
    else if (event.key === 'ArrowLeft') index = (index - 1 + tabs.length) % tabs.length;
    else if (event.key === 'Home') index = 0;
    else if (event.key === 'End') index = tabs.length - 1;
    else return;
    event.preventDefault();
    selectTask(tabs[index]);
    tabs[index].focus();
  });
}

const copyStatus = document.getElementById('copy-status');
for (const button of document.querySelectorAll('[data-copy]')) {
  button.hidden = false;
  let resetTimer;
  button.addEventListener('click', async () => {
    if (button.getAttribute('aria-disabled') === 'true') return;
    const code = document.getElementById(button.dataset.copy);
    clearTimeout(resetTimer);
    button.setAttribute('aria-disabled', 'true');
    button.setAttribute('aria-busy', 'true');
    button.textContent = 'Copying…';
    copyStatus.textContent = '';
    try {
      await navigator.clipboard.writeText(code.textContent.trim());
      button.textContent = 'Copied!';
      copyStatus.textContent = 'Command copied to clipboard.';
    } catch {
      // Select the command for a manual copy on file:// or restricted browsers.
      const range = document.createRange();
      range.selectNodeContents(code);
      const selection = window.getSelection();
      selection?.removeAllRanges();
      selection?.addRange(range);
      button.textContent = 'Selected';
      copyStatus.textContent = selection
        ? 'Clipboard unavailable. Command selected; use your browser’s Copy command.'
        : 'Clipboard unavailable. Select the command and use your browser’s Copy command.';
    } finally {
      button.removeAttribute('aria-disabled');
      button.removeAttribute('aria-busy');
    }
    resetTimer = setTimeout(() => { button.textContent = 'Copy'; }, 2500);
  });
}
