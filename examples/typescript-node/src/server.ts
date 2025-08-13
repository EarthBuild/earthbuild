export function sayHello(who?: string): string {
    who = who ?? 'earthbuild';
    return `Hello ${who}`;
}
