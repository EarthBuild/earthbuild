export default {
    transform: {
        '^.+\\.tsx?$': '@swc/jest',
    },
    testEnvironment: 'node',
    rootDir: 'src',
};
