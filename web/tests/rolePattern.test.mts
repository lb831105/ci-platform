import assert from 'node:assert/strict'
import test from 'node:test'
import { escapeRegExp, jobsFromPattern, patternFromJobs } from '../src/utils/rolePattern.ts'

test('escapeRegExp 让任务名按字面参与正则', () => {
  assert.equal(escapeRegExp('as.doc-x'), 'as\\.doc-x')
  assert.equal(escapeRegExp('a+b'), 'a\\+b')
  assert.equal(escapeRegExp('a(b)c'), 'a\\(b\\)c')
  assert.equal(escapeRegExp('中文任务_1'), '中文任务_1')
})

test('patternFromJobs 单任务 → 单名字，多任务 → 并集（排序去重）', () => {
  assert.equal(patternFromJobs(['as_doc_admin']), 'as_doc_admin')
  assert.equal(patternFromJobs(['b-job', 'a-job', 'b-job']), 'a-job|b-job')
  assert.equal(patternFromJobs(['as.doc']), 'as\\.doc')
  assert.equal(patternFromJobs([]), '')
  assert.equal(patternFromJobs(['  ']), '')
})

test('勾选与 Pattern 可往返，且生成的 Pattern 只匹配被选中的任务', () => {
  const jobs = ['as_doc_admin', 'as.doc-web', 'prod-deploy']
  const pattern = patternFromJobs(['as_doc_admin', 'as.doc-web'])
  // 后端锚定 (?i)^(?:pattern)$
  const re = new RegExp(`^(?:${pattern})$`, 'i')
  assert.deepEqual(jobs.filter((n) => re.test(n)), ['as_doc_admin', 'as.doc-web'])
  // 反推勾选状态
  assert.deepEqual(jobsFromPattern(pattern, jobs), ['as_doc_admin', 'as.doc-web'])
})

test('手写复杂正则不误判为勾选状态', () => {
  const jobs = ['as_doc_admin', 'as-doc-web']
  assert.deepEqual(jobsFromPattern('as[-_]doc-.+', jobs), [])
  assert.deepEqual(jobsFromPattern('', jobs), [])
})
