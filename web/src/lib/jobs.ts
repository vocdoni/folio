// SPDX-License-Identifier: AGPL-3.0-only
// Assistant jobs: start, then poll every 2 s until done (no SSE for jobs).

import { api, ApiError } from './api';
import type { Job } from './types';

export async function runJob<R>(path: string, body: unknown, onStep: (step: string) => void, signal?: AbortSignal): Promise<R> {
  const { job_id } = await api.post<{ job_id: string }>(path, body);
  for (;;) {
    await new Promise((res) => setTimeout(res, 2000));
    if (signal?.aborted) throw new DOMException('aborted', 'AbortError');
    const j = await api.get<Job<R>>(`/ai/jobs/${job_id}`);
    if (j.state === 'done') return j.result as R;
    if (j.state === 'error') throw new ApiError(200, j.error?.code ?? 'error', j.error?.message ?? 'The assistant failed.');
    onStep(j.step || (j.state === 'queued' ? 'waiting to start' : 'working'));
  }
}
