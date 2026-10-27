# TypeScript quickstart (Vitest / Jest)

Five minutes from zero to a passing trace assertion in Vitest or Jest.
Versions on this page: [llmentary for JS/TS](https://github.com/barzoj-co/llmentary-js)
**0.4.8**, which fetches proxy **v0.5.3**.

## 1. Install

Install the client, the OpenAI SDK and a local test runner:

=== "Vitest"

    ```sh
    npm install --save-dev llmentary openai "vitest@^3"
    ```

=== "Jest"

    ```sh
    npm install --save-dev llmentary openai "jest@^30"
    ```

llmentary needs Node 18 or newer. Vitest 3 and Jest 30 still run on Node 18;
newer Vitest majors need a newer Node. npm installs an optional `@llmentary/<platform>`
package with the proxy binary. Set `LLMENTARY_BIN` to use a different binary.

## 2. Wire up the test runner

=== "Vitest"

    ```ts
    // vitest.config.ts
    import { defineConfig } from "vitest/config";

    export default defineConfig({
      test: {
        setupFiles: ["./vitest.setup.ts"],
      },
    });
    ```

    ```ts
    // vitest.setup.ts
    import { setupVitest } from "llmentary/vitest";

    setupVitest();
    ```

    `setupVitest()` starts a proxy for the test file unless `LLMENTARY_PROXY_URL`
    is set, creates a fresh session before each test and deletes it after.
    Use `setupFiles`, not `globalSetup`.

=== "Jest"

    ```js
    // jest.config.js
    module.exports = {
      testEnvironment: "node",
      globalSetup: "llmentary/jest/globalSetup",
      globalTeardown: "llmentary/jest/globalTeardown",
      setupFilesAfterEnv: ["llmentary/jest/setup"],
    };
    ```

## 3. Write a test

=== "Vitest"

    ```ts
    // example.test.ts
    import OpenAI from "openai";
    import { test } from "vitest";
    import { llmentary } from "llmentary/vitest";

    test("uses the weather tool", async () => {
      const lm = llmentary();
      const openai = new OpenAI(lm.openAI());

      await openai.chat.completions.create({
        model: "gpt-4.1-mini",
        messages: [{ role: "user", content: "Use the tool for Amsterdam weather." }],
        tools: [
          {
            type: "function",
            function: {
              name: "lookup_weather",
              parameters: {
                type: "object",
                properties: { city: { type: "string" } },
                required: ["city"],
              },
            },
          },
        ],
        tool_choice: { type: "function", function: { name: "lookup_weather" } },
      });

      await lm.expect().toolCalled("lookup_weather", { argsSubset: { city: "Amsterdam" } });
    });
    ```

=== "Jest"

    ```js
    const OpenAI = require("openai");
    const { llmentary } = require("llmentary/jest");

    test("uses the weather tool", async () => {
      const lm = llmentary();
      const openai = new OpenAI(lm.openAI());

      await openai.chat.completions.create({
        model: "gpt-4.1-mini",
        messages: [{ role: "user", content: "Use the tool for Amsterdam weather." }],
        tools: [
          {
            type: "function",
            function: {
              name: "lookup_weather",
              parameters: {
                type: "object",
                properties: { city: { type: "string" } },
                required: ["city"],
              },
            },
          },
        ],
        tool_choice: { type: "function", function: { name: "lookup_weather" } },
      });

      await expect(lm.expect()).toHaveToolCalled("lookup_weather");
    });
    ```

`lm.openAI()` returns the `baseURL`, session headers and API key
(`OPENAI_API_KEY` from the environment) for `new OpenAI(...)`.

## 4. Run it

=== "Vitest"

    ```sh
    OPENAI_API_KEY=sk-... npx vitest run
    ```

=== "Jest"

    ```sh
    OPENAI_API_KEY=sk-... npx jest
    ```

With no upstream configured, the proxy forwards to
`openai=https://api.openai.com/v1`. Failures throw `SessionAssertionError` with a
trace summary.

## Next steps

- **Service can't send headers?** Inside the test, take the runner's session,
  set the env, and only then import the app:

    ```ts
    import { test } from "vitest";
    import { llmentary } from "llmentary/vitest";

    test("service without headers", async () => {
      const lm = llmentary();
      process.env.OPENAI_BASE_URL = lm.pathBaseURL();
      const { app } = await import("./app.js");
      // drive the app, then assert:
      await lm.expect().callCount(1);
    });
    ```

    More in [Black-box and sidecar setups](../black-box.md).

- **Scope one turn:** `const mark = await lm.mark();` then
  `await lm.expect({ since: mark }).callCount(1);`
- Full API: [TypeScript API reference](https://github.com/barzoj-co/llmentary-js/blob/main/docs/api.md).
