import config from "./jest.config.ts";

export default {
  ...config,
  rootDir: ".",
  testMatch: ["<rootDir>/integration/**/*.spec.ts"],
};
