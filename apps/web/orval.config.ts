import { defineConfig } from 'orval'

// The console client is generated from the API contract: `npm run generate`.
export default defineConfig({
  docibis: {
    input: { target: '../api/openapi.yaml' },
    output: {
      target: 'src/api/generated/docibis.ts',
      client: 'react-query',
      httpClient: 'fetch',
      mode: 'single',
      clean: true,
      override: {
        mutator: { path: 'src/api/fetcher.ts', name: 'apiFetch' },
        fetch: { includeHttpResponseReturnType: false },
      },
    },
  },
})
