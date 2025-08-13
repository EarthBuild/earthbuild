import {sayHello} from './server';

describe('sayHello', () => {
    it('should say Hello earthbuild if nothing is passed', () => {
        expect(sayHello()).toBe('Hello earthbuild');
    });

    it('should say Hello World if World is passed', () => {
        expect(sayHello('World')).toBe('Hello World');
    });
});
