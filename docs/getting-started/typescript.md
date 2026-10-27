# Get started with TypeScript

This page takes you from an empty directory to a passing Vitest test that calls
OpenAI through llmentary and checks which tool the model called. Jest setup is
at the end.

You need an OpenAI API key and Node.js. llmentary runs on Node.js 18 or newer.
llmentary is tested with Vitest 3. The current `openai` package (v7) requires
Node.js 22; on Node.js 18 or 20, install `openai@6`. The test makes one real request to `gpt-4.1-mini`.

## Install

```bash
npm init -y
npm pkg set type=module
npm install --save-dev llmentary openai vitest@3
```

npm also installs an optional `@llmentary/<platform>` package that contains the
proxy binary. To use a different binary, set `LLMENTARY_BIN`.

## Configure Vitest

```ts
// vitest.config.ts
import { defineConfig } from "vitest/config";

export default defineConfig({
  test: { setupFiles: ["./vitest.setup.ts"], testTimeout: 30_000 },
});
```

```ts
// vitest.setup.ts
import { setupVitest } from "llmentary/vitest";

setupVitest();
```

`setupVitest()` starts the proxy for each test file, creates a fresh session
before each test and deletes it afterwards. It has to be in `setupFiles`, not
`globalSetup`. The longer `testTimeout` leaves room for a live request; the
default is 5 seconds.

## Write a test

```ts
// weather.test.ts
import { test } from "vitest";
import OpenAI from "openai";
import { llmentary } from "llmentary/vitest";

test("the model calls the weather tool", async () => {
  const lm = llmentary();
  const openai = new OpenAI({ ...lm.openAI(), maxRetries: 0 });

  await openai.chat.completions.create({
    model: "gpt-4.1-mini",
    messages: [{ role: "user", content: "What's the weather in Paris?" }],
    tools: [{
      type: "function",
      function: {
        name: "get_weather",
        parameters: { type: "object", properties: { city: { type: "string" } }, required: ["city"] },
      },
    }],
    tool_choice: "required",
  });

  const check = lm.expect();
  await check.callCount(1);
  await check.toolCalled("get_weather", { argsSubset: { city: "Paris" } });
  await check.noErrors();
  await check.tokenBounds({ total: { max: 1000 } });
});
```

`lm.openAI()` returns `baseURL`, `apiKey` (from `OPENAI_API_KEY`) and
`defaultHeaders` for the `OpenAI` constructor. The `X-Test-Session` header in
`defaultHeaders` tells the proxy which session a request belongs to. The API
key is passed through to OpenAI unchanged.

`lm.expect()` waits until the session has had no new calls for 500 ms, then
each assertion checks the recorded calls and throws `SessionAssertionError` if
it fails.

## Run it

```console
$ export OPENAI_API_KEY=sk-...
$ npx vitest run

 Test Files  1 passed (1)
      Tests  1 passed (1)
```

## See a failure

Change `max: 1000` to `max: 10` and run again. The error lists the recorded
calls:

```text
SessionAssertionError: expected token bounds {…}
#1 (call_1) openai.chat model=gpt-4.1-mini-2025-04-14 tools=get_weather
   finish=tool_calls http=200 src=header tokens=43/14 error=no spec=clean
```

## Jest

```js
// jest.config.cjs (.cjs because package.json sets "type": "module")
module.exports = {
  testEnvironment: "node",
  globalSetup: "llmentary/jest/globalSetup",
  globalTeardown: "llmentary/jest/globalTeardown",
  setupFilesAfterEnv: ["llmentary/jest/setup"],
};
```

Install Jest with `npm install --save-dev jest`. In the test, import `llmentary` from `llmentary/jest` instead of
`llmentary/vitest`. The assertions are the same.

## Next steps

- [Connect an existing app](../how-to/connect-an-app.md) when the OpenAI client
  is created inside your application code.
- [Test one turn of a conversation](../how-to/scope-a-turn.md).
- [Assertions reference](../reference/assertions.md).
- TypeScript API details: [llmentary-js docs](https://github.com/barzoj-co/llmentary-js/blob/main/docs/api.md).
