// Drive the chosen composer's attachment action instead of depending on where
// its host-owned file input is mounted. Shared by renderer acceptance journeys.
async function attachComposerFiles(win, ownerSelector, files) {
  const select = async ({ ownerSelector, files }) => {
    const owners = document.querySelectorAll(ownerSelector);
    if (owners.length !== 1) throw new Error(`Expected one attachment owner: ${ownerSelector}, got ${owners.length}`);
    const owner = owners[0];
    const originalClick = HTMLInputElement.prototype.click;
    const clicked = [];
    HTMLInputElement.prototype.click = function () {
      if (this.type !== "file") return originalClick.call(this);
      clicked.push(this);
      // Keep the host's focus/ownership click handler, without opening an OS
      // dialog. Only the picker actually invoked by the action receives files.
      const event = new MouseEvent("click", { bubbles: true, cancelable: true });
      event.preventDefault();
      this.dispatchEvent(event);
    };
    try {
      const plus = owner.querySelector(".composer-plus-button");
      if (plus) {
        if (document.querySelector('[data-floating-menu-owner="composer-plus"]')) {
          throw new Error("Another composer attachment menu is already open");
        }
        plus.click();
        const deadline = performance.now() + 5000;
        let attachment;
        while (!attachment && performance.now() < deadline) {
          attachment = [...document.querySelectorAll('[data-floating-menu-owner="composer-plus"] [role="menuitem"]')]
            .find((button) => /^(添加附件|Add attachment)$/.test(button.querySelector(".composer-plus-menu-item-title")?.textContent ?? ""));
          if (!attachment) await new Promise((resolve) => setTimeout(resolve, 10));
        }
        if (!attachment) throw new Error("Attachment action did not appear for " + ownerSelector);
        attachment.click();
      } else {
        const attachment = owner.querySelector(".composer-attach-button");
        if (!attachment) throw new Error("Attachment action not found for " + ownerSelector);
        attachment.click();
      }
      if (clicked.length !== 1 || !clicked[0].isConnected) {
        throw new Error(`Expected one live picker invoked by ${ownerSelector}, got ${clicked.length}`);
      }
      const transfer = new DataTransfer();
      for (const file of files) transfer.items.add(new File([file.contents], file.name, { type: file.type }));
      clicked[0].files = transfer.files;
      clicked[0].dispatchEvent(new Event("change", { bubbles: true }));
    } finally {
      HTMLInputElement.prototype.click = originalClick;
    }
  };
  await win.webContents.executeJavaScript(`(${select})(${JSON.stringify({ ownerSelector, files })})`, true);
}

module.exports = { attachComposerFiles };
