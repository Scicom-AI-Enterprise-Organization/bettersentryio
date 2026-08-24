import React, { ChangeEvent } from 'react';

import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { Field, Input, SecretInput } from '@grafana/ui';

import { BsioDataSourceOptions, BsioSecureJsonData } from '../types';

type Props = DataSourcePluginOptionsEditorProps<BsioDataSourceOptions, BsioSecureJsonData>;

/**
 * Two fields, because connecting needs exactly two things: where the engine is, and
 * a token that may read it. The token goes into secureJsonData — encrypted at rest,
 * write-only from the browser, attached to requests by the data proxy — so it never
 * appears in the page or in a dashboard export.
 */
export function ConfigEditor({ options, onOptionsChange }: Props) {
  const { jsonData, secureJsonFields, secureJsonData } = options;

  const onUrl = (e: ChangeEvent<HTMLInputElement>) =>
    onOptionsChange({ ...options, jsonData: { ...jsonData, url: e.target.value.replace(/\/+$/, '') } });

  const onToken = (e: ChangeEvent<HTMLInputElement>) =>
    onOptionsChange({ ...options, secureJsonData: { ...secureJsonData, apiToken: e.target.value } });

  const onResetToken = () =>
    onOptionsChange({
      ...options,
      secureJsonFields: { ...secureJsonFields, apiToken: false },
      secureJsonData: { ...secureJsonData, apiToken: '' },
    });

  return (
    <>
      <Field
        label="Engine URL"
        description="The bettersentryio engine, not its web UI — e.g. http://host.docker.internal:9090 from a container."
      >
        <Input
          id="bsio-url"
          value={jsonData.url ?? ''}
          placeholder="http://host.docker.internal:9090"
          width={44}
          onChange={onUrl}
        />
      </Field>
      <Field
        label="API token"
        description="A bsiot_… token from Settings → API tokens. Read-only by construction — do not use the operator token here."
      >
        <SecretInput
          id="bsio-token"
          isConfigured={Boolean(secureJsonFields?.apiToken)}
          value={secureJsonData?.apiToken ?? ''}
          placeholder="bsiot_…"
          width={44}
          onChange={onToken}
          onReset={onResetToken}
        />
      </Field>
    </>
  );
}
