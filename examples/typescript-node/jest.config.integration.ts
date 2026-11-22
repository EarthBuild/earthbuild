export default {
  transform: {
    "^.+\\.tsx?$": "@swc/jest",
  },
  testEnvironment: "node",
  rootDir: ".",
  testMatch: ["<rootDir>/integration/**/*.spec.ts"],
};
