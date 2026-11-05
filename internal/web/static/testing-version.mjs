// Shared by the workspace and mobile to-do entry. A suggestion never attests
// that this code has actually been deployed to the selected environment.
export async function retestVersion(task, api) {
  try {
    const candidate = await api(`/tasks/${encodeURIComponent(task.id)}/retest-version`);
    return { ...candidate, hint: "已带出包含全部已验证修复的需求分支版本。请确认测试环境实际部署了此版本；也可填写其他包含全部修复的 SHA。" };
  } catch (error) {
    return { sha: "", version: task.version, revision: task.revision, hint: `未能自动带出复测版本：${error.message}。请同步仓库后重开，或填写已部署且包含全部修复的 SHA。` };
  }
}

// Preserve the request for uncertain transport retries. Editing any submitted
// field creates a new request instead of resending the cached old SHA/evidence.
export function testingRequest(form, data, createID) {
  const input = {
    action: form.dataset.operation || form.dataset.form,
    version: Number(form.dataset.version),
    body: data.note,
    test_verification: { revision: Number(form.dataset.revision), sha: data.sha?.trim(), environment: data.environment?.trim(), note: data.note },
  };
  const signature = JSON.stringify(input);
  if (form.dataset.testingInput !== signature) {
    form.dataset.testingInput = signature;
    form.dataset.payload = JSON.stringify({ ...input, request_id: createID() });
  }
  return JSON.parse(form.dataset.payload);
}
