<template>
	<GenericModal
		id="timeseriesModal"
		ref="modal"
		data-testid="timeseries-modal"
		:title="$t('config.timeseries.title')"
		size="lg"
		config-modal-name="timeseries"
		:prevent-dismiss="!nothingChanged"
		@open="open"
	>
		<p>
			{{ $t("config.timeseries.description") }}
			<a :href="docsLink" target="_blank" rel="noopener">
				{{ $t("config.general.docsLink") }}
			</a>
		</p>

		<p v-if="error" class="text-danger">
			{{ error }}
		</p>

		<form ref="form" class="container mx-0 px-0" @submit.prevent="save">
			<!-- InfluxDB Section -->
			<div class="card mb-4 border-0 bg-light p-3">
				<div class="d-flex justify-content-between align-items-center mb-3">
					<h5 class="m-0 d-flex align-items-center gap-2">
						<InfluxIcon />
						<span>{{ $t("config.timeseries.influxSection") }}</span>
					</h5>
					<span v-if="isInfluxConfigured" class="badge bg-success-subtle text-success">
						{{ $t("config.timeseries.configured") }}
					</span>
				</div>

				<FormRow
					id="influxUrl"
					:label="$t('config.influx.labelUrl')"
					example="http://localhost:8086"
					optional
				>
					<input
						id="influxUrl"
						v-model="influx.url"
						type="url"
						class="form-control"
					/>
				</FormRow>

				<template v-if="influx.url">
					<FormRow
						v-if="!showV1"
						id="influxOrg"
						:label="$t('config.influx.labelOrg')"
						example="home"
					>
						<input id="influxOrg" v-model="influx.org" class="form-control" :required="!showV1" />
					</FormRow>
					<FormRow
						id="influxDatabase"
						:label="$t(`config.influx.label${showV1 ? 'Database' : 'Bucket'}`)"
						example="evcc"
					>
						<input
							id="influxDatabase"
							v-model="influx.database"
							class="form-control"
							required
						/>
					</FormRow>
					<FormRow
						v-if="!showV1"
						id="influxToken"
						:label="$t('config.influx.labelToken')"
						:help="$t('config.influx.descriptionToken')"
					>
						<input id="influxToken" v-model="influx.token" class="form-control" :required="!showV1" />
					</FormRow>
					<FormRow
						v-if="showV1"
						id="influxUser"
						:label="$t('config.influx.labelUser')"
						optional
					>
						<input id="influxUser" v-model="influx.user" class="form-control" />
					</FormRow>
					<FormRow
						v-if="showV1"
						id="influxPassword"
						:label="$t('config.influx.labelPassword')"
						optional
					>
						<input
							id="influxPassword"
							v-model="influx.password"
							class="form-control"
							type="password"
							autocomplete="off"
						/>
					</FormRow>
					<FormRow id="influxInsecure" :label="$t('config.influx.labelInsecure')">
						<div class="d-flex">
							<input
								id="influxInsecure"
								v-model="influx.insecure"
								class="form-check-input"
								type="checkbox"
							/>
							<label class="form-check-label ms-2" for="influxInsecure">
								{{ $t("config.influx.labelCheckInsecure") }}
							</label>
						</div>
					</FormRow>
					<div class="d-flex justify-content-between align-items-center mt-2">
						<button
							v-if="showV1"
							class="btn btn-link btn-sm text-primary px-0"
							type="button"
							@click="
								influx.user = '';
								influx.password = '';
								v1 = false;
							"
						>
							{{ $t("config.influx.v2Support") }}
						</button>
						<button
							v-else
							class="btn btn-link btn-sm text-primary px-0"
							type="button"
							@click="
								influx.token = '';
								v1 = true;
							"
						>
							{{ $t("config.influx.v1Support") }}
						</button>
						<button
							v-if="isInfluxConfigured"
							type="button"
							class="btn btn-link btn-sm text-danger px-0"
							@click="clearInflux"
						>
							{{ $t("config.general.remove") }}
						</button>
					</div>
				</template>
			</div>

			<!-- OpenTelemetry Section -->
			<div class="card mb-4 border-0 bg-light p-3">
				<div class="d-flex justify-content-between align-items-center mb-3">
					<h5 class="m-0 d-flex align-items-center gap-2">
						<shopicon-regular-barchart class="text-primary" />
						<span>{{ $t("config.timeseries.opentelemetrySection") }}</span>
					</h5>
					<span v-if="isOtelConfigured" class="badge bg-success-subtle text-success">
						{{ $t("config.timeseries.configured") }}
					</span>
				</div>

				<FormRow
					id="otelUrl"
					:label="$t('config.opentelemetry.labelUrl')"
					:example="$t('config.opentelemetry.exampleUrl')"
					optional
				>
					<input
						id="otelUrl"
						v-model="opentelemetry.url"
						class="form-control"
					/>
				</FormRow>

				<template v-if="opentelemetry.url">
					<FormRow
						id="otelProtocol"
						:label="$t('config.opentelemetry.labelProtocol')"
					>
						<div class="d-flex gap-3 mt-1">
							<div class="form-check">
								<input
									id="otelProtocolGrpc"
									v-model="opentelemetry.protocol"
									value="grpc"
									class="form-check-input"
									type="radio"
								/>
								<label class="form-check-label" for="otelProtocolGrpc">
									{{ $t("config.opentelemetry.protocolGrpc") }}
								</label>
							</div>
							<div class="form-check">
								<input
									id="otelProtocolHttp"
									v-model="opentelemetry.protocol"
									value="http/protobuf"
									class="form-check-input"
									type="radio"
								/>
								<label class="form-check-label" for="otelProtocolHttp">
									{{ $t("config.opentelemetry.protocolHttp") }}
								</label>
							</div>
						</div>
					</FormRow>

					<FormRow
						id="otelServiceName"
						:label="$t('config.opentelemetry.labelServiceName')"
						example="evcc"
						optional
					>
						<input
							id="otelServiceName"
							v-model="opentelemetry.serviceName"
							class="form-control"
							placeholder="evcc"
						/>
					</FormRow>

					<FormRow
						id="otelHeaders"
						:label="$t('config.opentelemetry.labelHeaders')"
						:help="$t('config.opentelemetry.helpHeaders')"
						optional
					>
						<textarea
							id="otelHeaders"
							v-model="headersText"
							class="form-control"
							rows="2"
							placeholder="Authorization=Bearer <token>"
						></textarea>
					</FormRow>

					<FormRow id="otelInsecure" :label="$t('config.opentelemetry.labelInsecure')">
						<div class="d-flex">
							<input
								id="otelInsecure"
								v-model="opentelemetry.insecure"
								class="form-check-input"
								type="checkbox"
							/>
							<label class="form-check-label ms-2" for="otelInsecure">
								{{ $t("config.opentelemetry.labelCheckInsecure") }}
							</label>
						</div>
					</FormRow>

					<div v-if="isOtelConfigured" class="d-flex justify-content-end mt-2">
						<button
							type="button"
							class="btn btn-link btn-sm text-danger px-0"
							@click="clearOtel"
						>
							{{ $t("config.general.remove") }}
						</button>
					</div>
				</template>
			</div>

			<div class="mt-4 d-flex justify-content-between gap-2 flex-column flex-sm-row">
				<div class="d-flex justify-content-between order-2 order-sm-1 gap-2 flex-grow-1 flex-sm-grow-0">
					<button
						type="button"
						class="btn btn-link text-muted btn-cancel"
						data-bs-dismiss="modal"
					>
						{{ $t("config.general.cancel") }}
					</button>
					<button
						v-if="isInfluxConfigured || isOtelConfigured"
						type="button"
						class="btn btn-link text-danger"
						:disabled="removing"
						@click="clearAll"
					>
						<span
							v-if="removing"
							class="spinner-border spinner-border-sm"
							role="status"
							aria-hidden="true"
						></span>
						{{ $t("config.timeseries.clearAll") }}
					</button>
				</div>

				<button
					type="submit"
					class="btn btn-primary order-1 order-sm-2"
					:disabled="saving || nothingChanged"
				>
					<span
						v-if="saving"
						class="spinner-border spinner-border-sm"
						role="status"
						aria-hidden="true"
					></span>
					{{ $t("config.general.save") }}
				</button>
			</div>
		</form>
	</GenericModal>
</template>

<script>
import GenericModal from "../Helper/GenericModal.vue";
import FormRow from "./FormRow.vue";
import InfluxIcon from "../MaterialIcon/Influx.vue";
import api from "@/api";
import store from "@/store";
import deepClone from "@/utils/deepClone";
import { closeModal } from "@/configModal";
import { docsPrefix } from "@/i18n";

export default {
	name: "TimeSeriesModal",
	components: { GenericModal, FormRow, InfluxIcon },
	emits: ["changed"],
	data() {
		return {
			saving: false,
			removing: false,
			error: "",
			v1: false,
			influx: {
				url: "",
				org: "",
				database: "",
				token: "",
				user: "",
				password: "",
				insecure: false,
			},
			serverInflux: {},
			opentelemetry: {
				url: "",
				protocol: "grpc",
				headers: {},
				serviceName: "",
				insecure: false,
			},
			serverOtel: {},
			headersText: "",
		};
	},
	computed: {
		docsLink() {
			return `${docsPrefix()}/reference/configuration/influx`;
		},
		showV1() {
			return this.v1 || !!this.influx.user || !!this.influx.password;
		},
		isInfluxConfigured() {
			return !!this.serverInflux?.url;
		},
		isOtelConfigured() {
			return !!this.serverOtel?.url;
		},
		nothingChanged() {
			const influxSame = JSON.stringify(this.influx) === JSON.stringify(this.serverInflux);
			const otelCurrent = {
				...this.opentelemetry,
				headers: this.parseHeaders(this.headersText),
			};
			const otelSame = JSON.stringify(otelCurrent) === JSON.stringify(this.serverOtel);
			return influxSame && otelSame;
		},
	},
	methods: {
		open() {
			this.error = "";
			this.saving = false;
			this.removing = false;
			this.load();
		},
		load() {
			this.serverInflux = deepClone(store.state?.influx || {});
			this.influx = {
				url: this.serverInflux.url || "",
				org: this.serverInflux.org || "",
				database: this.serverInflux.database || "",
				token: this.serverInflux.token || "",
				user: this.serverInflux.user || "",
				password: this.serverInflux.password || "",
				insecure: !!this.serverInflux.insecure,
			};
			this.v1 = !!this.serverInflux.user || !!this.serverInflux.password;

			this.serverOtel = deepClone(store.state?.opentelemetry || {});
			this.opentelemetry = {
				url: this.serverOtel.url || "",
				protocol: this.serverOtel.protocol || "grpc",
				headers: deepClone(this.serverOtel.headers || {}),
				serviceName: this.serverOtel.serviceName || "",
				insecure: !!this.serverOtel.insecure,
			};
			this.headersText = this.formatHeaders(this.opentelemetry.headers);
		},
		formatHeaders(headers) {
			if (!headers) return "";
			return Object.entries(headers)
				.map(([k, v]) => `${k}=${v}`)
				.join("\n");
		},
		parseHeaders(text) {
			const result = {};
			if (!text) return result;
			for (const line of text.split("\n")) {
				const trimmed = line.trim();
				if (!trimmed) continue;
				const idx = trimmed.indexOf("=");
				if (idx !== -1) {
					result[trimmed.slice(0, idx).trim()] = trimmed.slice(idx + 1).trim();
				} else {
					const colonIdx = trimmed.indexOf(":");
					if (colonIdx !== -1) {
						result[trimmed.slice(0, colonIdx).trim()] = trimmed.slice(colonIdx + 1).trim();
					}
				}
			}
			return result;
		},
		clearInflux() {
			this.influx = {
				url: "",
				org: "",
				database: "",
				token: "",
				user: "",
				password: "",
				insecure: false,
			};
		},
		clearOtel() {
			this.opentelemetry = {
				url: "",
				protocol: "grpc",
				headers: {},
				serviceName: "",
				insecure: false,
			};
			this.headersText = "";
		},
		async clearAll() {
			this.removing = true;
			this.error = "";
			try {
				if (this.isInfluxConfigured) {
					await api.delete("/config/influx");
				}
				if (this.isOtelConfigured) {
					await api.delete("/config/opentelemetry");
				}
				this.$emit("changed");
				await closeModal();
			} catch (e) {
				console.error(e);
				this.error = e.message || "Failed to remove configurations";
			} finally {
				this.removing = false;
			}
		},
		async save() {
			this.saving = true;
			this.error = "";
			try {
				// Save or remove Influx
				if (this.influx.url) {
					const res = await api.post("/config/influx", this.influx, {
						validateStatus: (code) => [200, 202, 400].includes(code),
					});
					if (res.status === 400) {
						this.error = res.data.error;
						this.saving = false;
						return;
					}
				} else if (this.isInfluxConfigured) {
					await api.delete("/config/influx");
				}

				// Save or remove OpenTelemetry
				if (this.opentelemetry.url) {
					const otelPayload = {
						...this.opentelemetry,
						headers: this.parseHeaders(this.headersText),
					};
					const res = await api.post("/config/opentelemetry", otelPayload, {
						validateStatus: (code) => [200, 202, 400].includes(code),
					});
					if (res.status === 400) {
						this.error = res.data.error;
						this.saving = false;
						return;
					}
				} else if (this.isOtelConfigured) {
					await api.delete("/config/opentelemetry");
				}

				this.$emit("changed");
				await closeModal();
			} catch (e) {
				console.error(e);
				this.error = e.message || "Failed to save configuration";
			} finally {
				this.saving = false;
			}
		},
	},
};
</script>
