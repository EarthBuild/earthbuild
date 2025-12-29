# Next.js Example

This page will walk you through an example of how to build a Next.js hello world website using Earthly.

For this example, we are using the [basic-css](https://github.com/vercel/next.js/tree/canary/examples/basic-css) Next.js example that is included in [Vercel's Next.js repository](https://github.com/vercel/next.js).

This is a minimal Next.js example using Earthly to build the project.

Files of interest:

- `pages/index.tsx` — simple Hello World page
- `styles.module.css` — basic styling
- `package.json` — includes `dev`, `build`, and `start` scripts
- `Earthfile` — builds the project using `npm run build`

Build locally with npm:

```bash
npm install
npm run build
npm run start
```

Or build using Earthly (the `build` target runs `npm run build` and caches artifacts):

```bash
earthly +build
```

Run production server:

```bash
earthly +image
docker run -p 3000:3000 nextjs-app:latest
```
