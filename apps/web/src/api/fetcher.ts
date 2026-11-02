import type { Problem } from './generated/docibis'

/** An API error, with the RFC 9457 problem the server sent when there is one. */
export class ApiError<P = Problem> extends Error {
  readonly status: number
  readonly problem?: P

  constructor(status: number, problem?: P, message?: string) {
    super(message ?? `HTTP ${status}`)
    this.name = 'ApiError'
    this.status = status
    this.problem = problem
  }
}

/** Error type of the generated hooks (orval reads this name). */
export type ErrorType<P> = ApiError<P>

/** Calls the API on the same address as the console (no CORS). */
export async function apiFetch<T>(url: string, options?: RequestInit): Promise<T> {
  const response = await fetch(url, { ...options, credentials: 'same-origin' })
  const text = [204, 205, 304].includes(response.status) ? '' : await response.text()
  const type = response.headers.get('Content-Type') ?? ''
  const body: unknown = text && type.includes('json') ? JSON.parse(text) : undefined
  if (!response.ok) {
    const problem = type.includes('problem+json') ? (body as Problem) : undefined
    throw new ApiError(response.status, problem, problem?.detail ?? problem?.title)
  }
  return body as T
}
