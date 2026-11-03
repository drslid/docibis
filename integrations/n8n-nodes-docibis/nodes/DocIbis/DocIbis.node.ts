import type {
	IDataObject,
	IExecuteFunctions,
	IHttpRequestOptions,
	INodeExecutionData,
	INodeType,
	INodeTypeDescription,
} from 'n8n-workflow';
import { NodeOperationError } from 'n8n-workflow';

// DocIbis in n8n: produce a document from a template of the library and JSON
// data, at once or in the background (job, presigned upload, callback), and
// read the published templates.

const renderOperations = ['render', 'renderAsync'];

function fileNameOf(disposition: unknown, fallback: string): string {
	const match = /filename\*?=(?:UTF-8'')?"?([^";]+)"?/i.exec(String(disposition ?? ''));
	return match ? decodeURIComponent(match[1]) : fallback;
}

export class DocIbis implements INodeType {
	description: INodeTypeDescription = {
		displayName: 'DocIbis',
		name: 'docIbis',
		icon: 'file:docibis.svg',
		group: ['transform'],
		version: 1,
		subtitle: '={{$parameter["operation"]}}',
		description: 'PDF and Word documents from Word templates (Docmosis tags) and JSON data',
		defaults: { name: 'DocIbis' },
		inputs: ['main'],
		outputs: ['main'],
		credentials: [{ name: 'docIbisApi', required: true }],
		properties: [
			{
				displayName: 'Operation',
				name: 'operation',
				type: 'options',
				noDataExpression: true,
				default: 'render',
				options: [
					{ name: 'Render', value: 'render', action: 'Render a document', description: 'Produce the document now (binary output)' },
					{ name: 'Render in Background', value: 'renderAsync', action: 'Render a document in the background', description: 'Get a job at once; the document is read later, uploaded, or announced by a callback' },
					{ name: 'Get Job', value: 'getJob', action: 'Get a background job' },
					{ name: 'Download Job Document', value: 'downloadJob', action: 'Download the document of a job' },
					{ name: 'List Templates', value: 'listTemplates', action: 'List the published templates' },
				],
			},
			{
				displayName: 'Template Name',
				name: 'templateName',
				type: 'string',
				default: '',
				required: true,
				placeholder: 'contracts/offer.docx',
				description: 'Path of the template in the DocIbis library (its published version is used)',
				displayOptions: { show: { operation: renderOperations } },
			},
			{
				displayName: 'Data (JSON)',
				name: 'data',
				type: 'json',
				default: '{}',
				description: 'The data of the document: an object whose keys are the tags of the template',
				displayOptions: { show: { operation: renderOperations } },
			},
			{
				displayName: 'Format',
				name: 'format',
				type: 'options',
				default: 'pdf',
				options: [
					{ name: 'PDF', value: 'pdf' },
					{ name: 'Word (DOCX)', value: 'docx' },
				],
				displayOptions: { show: { operation: renderOperations } },
			},
			{
				displayName: 'Job ID',
				name: 'jobId',
				type: 'string',
				default: '',
				required: true,
				placeholder: 'job_…',
				displayOptions: { show: { operation: ['getJob', 'downloadJob'] } },
			},
			{
				displayName: 'Put Output File in Field',
				name: 'binaryPropertyName',
				type: 'string',
				default: 'data',
				displayOptions: { show: { operation: ['render', 'downloadJob'] } },
			},
			{
				displayName: 'Options',
				name: 'options',
				type: 'collection',
				placeholder: 'Add option',
				default: {},
				displayOptions: { show: { operation: renderOperations } },
				options: [
					{ displayName: 'Output Name', name: 'outputName', type: 'string', default: '', placeholder: 'offer-1234.pdf' },
					{
						displayName: 'PDF/A',
						name: 'pdfa',
						type: 'options',
						default: 'none',
						options: [
							{ name: 'None', value: 'none' },
							{ name: 'PDF/A-1b', value: 'PDF/A-1b' },
							{ name: 'PDF/A-2b', value: 'PDF/A-2b' },
							{ name: 'PDF/A-3b', value: 'PDF/A-3b' },
						],
					},
					{ displayName: 'Accessible (PDF/UA)', name: 'accessible', type: 'boolean', default: false },
					{ displayName: 'Title', name: 'title', type: 'string', default: '' },
					{ displayName: 'Watermark', name: 'watermark', type: 'string', default: '', description: 'Text of the watermark; none for none' },
					{
						displayName: 'Data Check',
						name: 'dataCheck',
						type: 'options',
						default: 'warn',
						options: [
							{ name: 'Off', value: 'off' },
							{ name: 'Warn', value: 'warn' },
							{ name: 'Strict', value: 'strict' },
						],
					},
					{
						displayName: 'Callback URL',
						name: 'callbackUrl',
						type: 'string',
						default: '',
						description: 'Render in Background: HTTPS address called with the job when it ends (domain allowed in DocIbis)',
					},
					{
						displayName: 'Upload URL',
						name: 'uploadUrl',
						type: 'string',
						default: '',
						description: 'Render in Background: HTTPS address where DocIbis puts the document (a presigned S3 URL, for instance)',
					},
				],
			},
		],
	};

	async execute(this: IExecuteFunctions): Promise<INodeExecutionData[][]> {
		const items = this.getInputData();
		const credentials = await this.getCredentials('docIbisApi');
		const base = String(credentials.baseUrl).replace(/\/$/, '');
		const out: INodeExecutionData[] = [];
		const call = async (options: IHttpRequestOptions) =>
			this.helpers.httpRequestWithAuthentication.call(this, 'docIbisApi', options);

		for (let i = 0; i < items.length; i++) {
			try {
				const operation = this.getNodeParameter('operation', i) as string;
				if (operation === 'listTemplates') {
					const templates = (await call({ method: 'GET', url: `${base}/v1/templates`, json: true })) as IDataObject[];
					for (const t of templates) out.push({ json: t, pairedItem: { item: i } });
					continue;
				}
				if (operation === 'getJob' || operation === 'downloadJob') {
					const id = encodeURIComponent(this.getNodeParameter('jobId', i) as string);
					if (operation === 'getJob') {
						out.push({ json: (await call({ method: 'GET', url: `${base}/v1/jobs/${id}`, json: true })) as IDataObject, pairedItem: { item: i } });
						continue;
					}
					const res = await call({ method: 'GET', url: `${base}/v1/jobs/${id}/document`, encoding: 'arraybuffer', returnFullResponse: true });
					const property = this.getNodeParameter('binaryPropertyName', i) as string;
					const type = String(res.headers['content-type'] ?? 'application/pdf');
					const binary = await this.helpers.prepareBinaryData(Buffer.from(res.body as ArrayBuffer), fileNameOf(res.headers['content-disposition'], 'document'), type);
					out.push({ json: { jobId: id }, binary: { [property]: binary }, pairedItem: { item: i } });
					continue;
				}

				const templateName = this.getNodeParameter('templateName', i) as string;
				const data = this.getNodeParameter('data', i);
				const format = this.getNodeParameter('format', i) as string;
				const options = this.getNodeParameter('options', i, {}) as IDataObject;
				const json = typeof data === 'string' ? data : JSON.stringify(data);
				try {
					JSON.parse(json);
				} catch {
					throw new NodeOperationError(this.getNode(), 'Data (JSON) is not valid JSON', { itemIndex: i });
				}
				const form = new FormData();
				form.append('templateName', templateName);
				form.append('data', json);
				form.append('format', format);
				for (const [name, value] of Object.entries(options)) {
					if (value === '' || value === undefined || value === null) continue;
					if (operation === 'render' && (name === 'callbackUrl' || name === 'uploadUrl')) continue;
					form.append(name, String(value));
				}
				if (operation === 'renderAsync') {
					const job = (await call({ method: 'POST', url: `${base}/v1/render/async`, body: form, json: true })) as IDataObject;
					out.push({ json: job, pairedItem: { item: i } });
					continue;
				}
				const res = await call({ method: 'POST', url: `${base}/v1/render`, body: form, encoding: 'arraybuffer', returnFullResponse: true });
				const property = this.getNodeParameter('binaryPropertyName', i) as string;
				const type = format === 'docx' ? 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' : 'application/pdf';
				const name = fileNameOf(res.headers['content-disposition'], `document.${format}`);
				const binary = await this.helpers.prepareBinaryData(Buffer.from(res.body as ArrayBuffer), name, type);
				out.push({
					json: {
						templateName,
						fileName: name,
						renderMs: Number(res.headers['x-render-ms'] ?? 0),
						engine: res.headers['x-render-engine'] ?? 'standard',
						missingFields: res.headers['x-missing-fields'] ? String(res.headers['x-missing-fields']).split(',').map(decodeURIComponent) : [],
					},
					binary: { [property]: binary },
					pairedItem: { item: i },
				});
			} catch (error) {
				if (this.continueOnFail()) {
					out.push({ json: { error: (error as Error).message }, pairedItem: { item: i } });
					continue;
				}
				throw error;
			}
		}
		return [out];
	}
}
