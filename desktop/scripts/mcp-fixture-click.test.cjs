const assert = require("node:assert/strict");
const { test } = require("node:test");
const vm = require("node:vm");
const clickReady = require("./fixtures/mcp-click.cjs");

function renderer(read) {
  return vm.runInNewContext(`(${clickReady})`, { document: { querySelectorAll: read } });
}
test("waits for a replacement control and dispatches once in the same renderer turn", () => {
  let controls = [], clicks = 0;
  const click = renderer(() => controls), request = { selector: "row button", label: "Login" };
  assert.equal(click(request), false);
  controls = [{ textContent: "Login", disabled: true, getAttribute: () => null, click: () => { clicks++; } }];
  assert.equal(click(request), false);
  controls = [{ ...controls[0], disabled: false }];
  assert.equal(click(request), true);
  assert.equal(clicks, 1);
});
test("a synchronous focus event cannot reenter the waiter and click twice", () => {
  let clicks = 0;
  const request = { selector: "button" };
  const click = renderer(() => [{ getAttribute: () => null, click: () => { clicks++; if (clicks === 1) click(request); } }]);
  assert.equal(click(request), true);
  assert.equal(clicks, 1);
});
test("an uncertain dispatch failure is not retried", () => {
  let clicks = 0;
  const request = { selector: "button" };
  const click = renderer(() => [{ getAttribute: () => null, click: () => { clicks++; throw new Error("uncertain outcome"); } }]);
  assert.throws(() => click(request), /uncertain outcome/);
  assert.equal(click(request), true);
  assert.equal(clicks, 1);
});
