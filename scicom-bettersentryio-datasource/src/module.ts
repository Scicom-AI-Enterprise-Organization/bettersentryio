import { DataSourcePlugin } from '@grafana/data';

import { ConfigEditor } from './components/ConfigEditor';
import { QueryEditor } from './components/QueryEditor';
import { BsioDataSource } from './datasource';
import { BsioDataSourceOptions, BsioQuery } from './types';

export const plugin = new DataSourcePlugin<BsioDataSource, BsioQuery, BsioDataSourceOptions>(BsioDataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
